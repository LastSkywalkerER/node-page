// Package server provides the HTTP server implementation for the system statistics API.
// This package handles HTTP request routing, middleware setup, and graceful shutdown
// for the system monitoring application, serving both REST API endpoints and static files.
package server

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/charmbracelet/log"
	"github.com/gin-gonic/gin"
	ginSwagger "github.com/swaggo/gin-swagger"
	swaggerFiles "github.com/swaggo/files"

	_ "system-stats/docs"
	"system-stats/internal/app/config"
	"system-stats/internal/app/di"
	"system-stats/internal/app/help"
	"system-stats/internal/app/middleware"
	"system-stats/internal/app/prometheusmetrics"
	"system-stats/internal/app/pusher"
	clusterconfig "system-stats/internal/modules/nodes/infrastructure/cluster_config"
	"system-stats/internal/app/retention"
	historyapp "system-stats/internal/modules/history_metrics/application"
	cpumodule "system-stats/internal/modules/cpu/presentation"
	diskmodule "system-stats/internal/modules/disk/presentation"
	dockermodule "system-stats/internal/modules/docker/presentation"
	healthmodule "system-stats/internal/modules/health/presentation"
	hostmodule "system-stats/internal/modules/hosts/presentation"
	invmodule "system-stats/internal/modules/invitations/presentation"
	nodesmodule "system-stats/internal/modules/nodes/presentation"
	memorymodule "system-stats/internal/modules/memory/presentation"
	networkmodule "system-stats/internal/modules/network/presentation"
	sensorsmodule "system-stats/internal/modules/sensors/presentation"
	setupapp "system-stats/internal/modules/setup/application"
	setupmodule "system-stats/internal/modules/setup/presentation"
	streammodule "system-stats/internal/modules/stream/presentation"
	systemmodule "system-stats/internal/modules/system/presentation"
	usermodule "system-stats/internal/modules/users/presentation"
)

// Run starts the system statistics HTTP server.
func Run() {
	showHelp := flag.Bool("help", false, "Show help message with environment variables description")
	flag.Parse()

	if *showHelp {
		help.ShowAndExit()
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load configuration: %v\n", err)
		os.Exit(1)
	}

	logger := log.NewWithOptions(os.Stderr, log.Options{
		ReportCaller:    true,
		ReportTimestamp: true,
		Prefix:          "system-stats",
	})
	log.SetDefault(logger)

	if cfg.Debug {
		log.SetLevel(log.DebugLevel)
	} else {
		log.SetLevel(log.InfoLevel)
	}

	if cfg.GinMode == "release" {
		gin.SetMode(gin.ReleaseMode)
	} else {
		gin.SetMode(gin.DebugMode)
	}

	startTime := time.Now()

	logger.Info("Initializing dependency injection container...", "db_type", cfg.Database.Type, "db_dsn", config.MaskDSN(cfg.Database.DSN))
	container, err := di.NewContainer(logger, cfg.Database, cfg.JWTSecret, cfg.RefreshSecret, startTime)
	if err != nil {
		logger.Fatal("Failed to initialize DI container", "error", err)
	}
	logger.Info("DI container initialized", "database_type", cfg.Database.Type)

	regCtx, regCancel := context.WithTimeout(context.Background(), 30*time.Second)
	if _, err := container.GetHostService().RegisterOrUpdateCurrentHost(regCtx); err != nil {
		logger.Error("Failed to register local collector host (fixed host_id=1)", "error", err)
	}
	regCancel()

	// Load cluster config from .env (MAIN_NODE_URL, NODE_ACCESS_TOKEN) for agent push mode
	clusterconfig.LoadFromEnvFile()

	historicalMetricsService := container.GetHistoricalMetricsService()

	// Wire SSE broker into the after-collect hook (harmless before collection starts).
	broker := container.GetBroker()
	systemSvc := container.GetSystemService()
	historicalMetricsService = historyapp.WithAfterCollect(historicalMetricsService, func() {
		metrics, err := systemSvc.CollectAllCurrent(context.Background())
		if err != nil {
			return
		}
		data, err := json.Marshal(metrics)
		if err != nil {
			return
		}
		var envelope map[string]interface{}
		if err := json.Unmarshal(data, &envelope); err != nil {
			return
		}
		if host, herr := container.GetHostService().GetCurrentHost(context.Background()); herr == nil && host != nil {
			envelope["collecting_host_id"] = host.ID
		}
		out, err := json.Marshal(envelope)
		if err != nil {
			return
		}
		var s struct {
			CPU    struct{ UsagePercent float64 `json:"usage_percent"` } `json:"cpu"`
			Memory struct{ UsagePercent float64 `json:"usage_percent"` } `json:"memory"`
			Docker struct{ RunningContainers int `json:"running_containers"` } `json:"docker"`
		}
		if err := json.Unmarshal(data, &s); err == nil {
			logger.Info("Metrics collected",
				"cpu", fmt.Sprintf("%.1f%%", s.CPU.UsagePercent),
				"mem", fmt.Sprintf("%.1f%%", s.Memory.UsagePercent),
				"containers", s.Docker.RunningContainers,
			)
		}
		broker.Publish(out)

		// Push to main node if cluster config is set (from env at startup or after connect)
		if mainURL, token := clusterconfig.Get(); mainURL != "" && token != "" {
			go pusher.Push(context.Background(), logger, mainURL, token, metrics)
		}
	})

	// startMetrics activates periodic collection and retention.
	// Called immediately on normal startup, or as a callback once setup completes.
	startMetrics := func() {
		logger.Info("Checking system stats availability...")
		if _, err := container.GetCPUService().Collect(context.Background()); err != nil {
			logger.Warn("Failed to get initial CPU stats, continuing without check", "error", err)
		} else {
			logger.Info("System stats are available")
		}

		logger.Info("Starting periodic metrics collection...")
		if err := historicalMetricsService.StartPeriodicCollection(context.Background(), 5*time.Second); err != nil {
			logger.Error("Failed to start periodic collection", "error", err)
			return
		}

		retentionSvc := retention.NewService(container.GetDB(), logger, cfg.RetentionDays)
		retentionSvc.Start(context.Background())
	}

	// Check if setup is needed (no users yet).
	userCount, err := container.GetUserService().Count(context.Background())
	if err != nil {
		logger.Fatal("Failed to check setup status", "error", err)
	}
	setupMode := userCount == 0
	if !setupMode {
		if err := cfg.RequireAuthSecrets(); err != nil {
			logger.Fatal("Invalid configuration for existing installation", "error", err)
		}
	}

	// onSetupComplete is passed to the setup handler and called once setup finishes.
	var onSetupComplete func()
	if setupMode {
		logger.Info("Setup mode: waiting for initial setup to complete before collecting metrics")
		onSetupComplete = func() {
			logger.Info("Setup completed — starting metrics collection")
			startMetrics()
		}
	} else {
		startMetrics()
	}

	router := setupRouter(container, startTime, logger, cfg, onSetupComplete)

	server := &http.Server{
		Addr:         cfg.Addr,
		Handler:      router,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		logger.Info("Starting server", "address", cfg.Addr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("Server error", "error", err)
		}
	}()

	<-quit
	logger.Info("Received interrupt signal, shutting down gracefully...")

	historicalMetricsService.StopPeriodicCollection()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error("Server forced to shutdown", "error", err)
	} else {
		logger.Info("Server exited gracefully")
	}
}

// setupRouter configures the Gin router with all routes, middleware, and handlers.
func setupRouter(container *di.Container, startTime time.Time, logger *log.Logger, cfg *config.Config, onSetupComplete func()) *gin.Engine {
	router := gin.New()
	router.Use(gin.Recovery())
	router.Use(middleware.ErrorHandler())

	var promHandler *prometheusmetrics.Metrics
	if cfg.PrometheusEnabled {
		promHandler = prometheusmetrics.New(
			container.GetCPUService(),
			container.GetMemoryService(),
			container.GetDiskService(),
			container.GetNetworkService(),
		)
		router.Use(promHandler.GinMiddleware())
	}

	router.Use(middleware.LoggingMiddleware(logger))
	router.Use(middleware.CORSMiddleware(cfg.AllowOrigin, cfg.AllowOrigin != "*"))

	wd, err := os.Getwd()
	if err != nil {
		logger.Fatal("Failed to get working directory", "error", err)
	}

	distPath := filepath.Join(wd, "dist")
	logger.Info("Serving static files", "path", distPath)

	systemHandler := systemmodule.NewSystemHandler(logger, container.GetSystemService(), container.GetHostService())
	cpuHandler := cpumodule.NewCPUHandler(logger, container.GetCPUService(), container.GetHostService())
	memoryHandler := memorymodule.NewMemoryHandler(logger, container.GetMemoryService(), container.GetHostService())
	diskHandler := diskmodule.NewDiskHandler(logger, container.GetDiskService(), container.GetHostService())
	networkHandler := networkmodule.NewNetworkHandler(logger, container.GetNetworkService(), container.GetHostService())
	dockerHandler := dockermodule.NewDockerHandler(logger, container.GetDockerService(), container.GetHostService())
	sensorsHandler := sensorsmodule.NewSensorsHandler(logger, container.GetSensorsService(), container.GetHostService())
	hostHandler := hostmodule.NewHostHandler(logger, container.GetHostService())
	healthHandler := healthmodule.NewHealthHandler(logger, container.GetHealthService())
	authHandler := usermodule.NewAuthHandler(container.GetUserService(), container.GetTokenService(), cfg.CookieSecure)
	usersHandler := usermodule.NewUsersHandler(container.GetUserService())
	invitationHandler := invmodule.NewInvitationHandler(container.GetInvitationService())
	nodesHandler := nodesmodule.NewNodesHandler(container.GetNodeService(), container.GetHostService(), cfg.PublicBaseURL)
	streamHandler := streammodule.NewStreamHandler(container.GetBroker(), container.GetHostService())
	configWriter := setupapp.NewConfigWriter()
	setupHandler := setupmodule.NewSetupHandler(configWriter, container.GetUserService(), onSetupComplete)

	// Swagger UI (always available)
	router.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))

	api := router.Group("/api/v1")
	{
		// Prometheus metrics (scraped by Prometheus server)
		if promHandler != nil {
			metricsHandlers := []gin.HandlerFunc{}
			if cfg.PrometheusAuth && cfg.PrometheusToken != "" {
				metricsHandlers = append(metricsHandlers, middleware.AuthBearerToken(cfg.PrometheusToken))
			}
			metricsHandlers = append(metricsHandlers, gin.WrapH(promHandler.Handler()))
			api.GET("/metrics", metricsHandlers...)
			logger.Info("Prometheus metrics enabled", "endpoint", "/api/v1/metrics", "auth", cfg.PrometheusAuth)
		}

		// Public: health check (no auth — used by load balancers and k8s probes)
		api.GET("/health", healthHandler.HandleHealth)

		// Setup routes (public, only work when no users exist)
		setup := api.Group("/setup")
		{
			setup.GET("/status", setupHandler.Status)
			setup.GET("/config", setupHandler.GetConfig)
			setup.POST("/complete", setupHandler.CompleteSetup)
		}

		// Auth routes (public, rate-limited: 10 req/min per IP)
		authRL := middleware.RateLimitMiddleware(10.0/60, 10)
		api.GET("/invitations/validate", authRL, invitationHandler.ValidateInvitation)

		auth := api.Group("/auth")
		{
			auth.POST("/register", authRL, authHandler.Register)
			auth.POST("/login", authRL, authHandler.Login)
			auth.POST("/refresh", authRL, authHandler.Refresh)
			auth.POST("/logout", middleware.AuthJWT(container.GetTokenService()), authHandler.Logout)
		}

		// User management routes (protected)
		users := api.Group("/users", middleware.AuthJWT(container.GetTokenService()))
		{
			users.GET("/me", usersHandler.Me)
			users.GET("", middleware.RequireAdmin(), usersHandler.List)
			users.PATCH("/:id", middleware.RequireAdmin(), usersHandler.UpdateRole)
			users.DELETE("/:id", middleware.RequireAdmin(), usersHandler.Delete)
		}

		// Invitations (admin only)
		invitations := api.Group("/invitations", middleware.AuthJWT(container.GetTokenService()), middleware.RequireAdmin())
		{
			invitations.POST("", invitationHandler.CreateInvitation)
		}

		// Node join (public — agent calls this to register with main)
		nodes := api.Group("/nodes")
		{
			nodes.POST("/join", nodesHandler.Join)
		}

		// Node push (auth via node_access_token)
		nodesPush := api.Group("/nodes", middleware.AuthNodeToken(container.GetNodeService()))
		{
			nodesPush.POST("/push", nodesHandler.Push)
		}

		// Metrics current snapshot
		api.GET("/metrics/current", middleware.AuthJWT(container.GetTokenService()), systemHandler.HandleCurrentMetrics)
	}

	// Individual metrics routes (all protected)
	authAPI := api.Group("", middleware.AuthJWT(container.GetTokenService()))
	{
		authAPI.GET("/cpu", cpuHandler.HandleCPUStats)
		authAPI.GET("/memory", memoryHandler.HandleMemoryStats)
		authAPI.GET("/disk", diskHandler.HandleDiskStats)
		authAPI.GET("/network", networkHandler.HandleNetworkStats)
		authAPI.GET("/docker", dockerHandler.HandleDockerStats)
		authAPI.GET("/sensors", sensorsHandler.HandleSensors)
		authAPI.GET("/hosts", hostHandler.HandleGetAllHosts)
		authAPI.GET("/hosts/current", hostHandler.HandleGetCurrentHost)
		authAPI.POST("/hosts/register", hostHandler.HandleRegisterCurrentHost)
		authAPI.GET("/stream", streamHandler.HandleStream)

		// Node invite (admin only)
		authAPI.POST("/nodes/invite", middleware.RequireAdmin(), nodesHandler.CreateInvite)
		// Agent manual setup on main (admin): URLs + regenerate push token
		authAPI.GET("/nodes/cluster-ui-status", middleware.RequireAdmin(), nodesHandler.GetClusterUIStatus)
		authAPI.PUT("/nodes/agent-cluster-config", middleware.RequireAdmin(), nodesHandler.UpdateAgentClusterConfig)
		authAPI.DELETE("/nodes/agent-cluster-config", middleware.RequireAdmin(), nodesHandler.DeleteAgentClusterConfig)
		authAPI.POST("/nodes/hosts/:id/regenerate-token", middleware.RequireAdmin(), nodesHandler.RegenerateAgentToken)
		authAPI.DELETE("/nodes/hosts/:id", middleware.RequireAdmin(), nodesHandler.DeleteRemoteHost)
		// Node connect (agent connects to main using join link)
		authAPI.POST("/nodes/connect", nodesHandler.Connect)
	}

	// Static files for React app (hashed bundles from Vite)
	router.Static("/assets", filepath.Join(distPath, "assets"))

	// SPA fallback routing
	router.NoRoute(func(c *gin.Context) {
		if strings.HasPrefix(c.Request.URL.Path, "/api/") {
			c.JSON(404, gin.H{"error": "API endpoint not found"})
			return
		}
		m := c.Request.Method
		if m == http.MethodGet || m == http.MethodHead {
			if abs, ok := resolveDistStaticFile(distPath, c.Request.URL.Path); ok {
				c.File(abs)
				return
			}
		}
		if strings.HasPrefix(c.Request.URL.Path, "/assets/") {
			c.Status(404)
			return
		}
		if pathLooksLikeMissingStaticAsset(c.Request.URL.Path) {
			c.Status(404)
			return
		}
		c.File(filepath.Join(distPath, "index.html"))
	})

	return router
}

// resolveDistStaticFile serves a single file from dist root (Vite copies frontend/public there on build).
func resolveDistStaticFile(distPath, urlPath string) (absFile string, ok bool) {
	rel := strings.TrimPrefix(urlPath, "/")
	if rel == "" || strings.Contains(rel, "..") {
		return "", false
	}
	candidate := filepath.Join(distPath, filepath.FromSlash(rel))
	absDist, err := filepath.Abs(distPath)
	if err != nil {
		return "", false
	}
	absFile, err = filepath.Abs(candidate)
	if err != nil {
		return "", false
	}
	relResult, err := filepath.Rel(absDist, absFile)
	if err != nil || strings.HasPrefix(relResult, "..") {
		return "", false
	}
	fi, err := os.Stat(absFile)
	if err != nil || fi.IsDir() {
		return "", false
	}
	return absFile, true
}

func pathLooksLikeMissingStaticAsset(urlPath string) bool {
	baseName := filepath.Base(strings.Split(urlPath, "?")[0])
	if baseName == "" || baseName == "." || baseName == "/" {
		return false
	}
	i := strings.LastIndex(baseName, ".")
	if i < 0 {
		return false
	}
	ext := strings.ToLower(baseName[i:])
	switch ext {
	case ".jpg", ".jpeg", ".png", ".gif", ".webp", ".ico", ".svg", ".webmanifest", ".json", ".css", ".js", ".map", ".woff", ".woff2", ".ttf", ".txt":
		return true
	default:
		return false
	}
}
