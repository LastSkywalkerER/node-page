// Package server provides the HTTP server implementation for the system statistics API.
// This package handles HTTP request routing, middleware setup, and graceful shutdown
// for the system monitoring application, serving both REST API endpoints and static files.
package server

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/charmbracelet/log"
	"github.com/gin-contrib/gzip"
	"github.com/gin-gonic/gin"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
	"gorm.io/gorm"

	_ "system-stats/docs"
	"system-stats/internal/app/config"
	"system-stats/internal/app/di"
	"system-stats/internal/app/help"
	"system-stats/internal/app/middleware"
	"system-stats/internal/app/prometheusmetrics"
	"system-stats/internal/app/retention"
	invitations "system-stats/internal/auth/invitations"
	users "system-stats/internal/auth/users"
	hosts "system-stats/internal/cluster/hosts"
	raftcluster "system-stats/internal/cluster/raft"
	metricstream "system-stats/internal/cluster/raft/metricstream"
	cpu "system-stats/internal/metrics/cpu"
	disk "system-stats/internal/metrics/disk"
	docker "system-stats/internal/metrics/docker"
	memory "system-stats/internal/metrics/memory"
	network "system-stats/internal/metrics/network"
	proxmox "system-stats/internal/metrics/proxmox"
	sensors "system-stats/internal/metrics/sensors"
	appicons "system-stats/internal/platform/appicons"
	connectors "system-stats/internal/platform/connectors"
	health "system-stats/internal/platform/health"
	history "system-stats/internal/platform/history"
	"system-stats/internal/platform/runtimetune"
	setup "system-stats/internal/platform/setup"
	platformstream "system-stats/internal/platform/stream"
	system "system-stats/internal/platform/system"
	"system-stats/internal/platform/update"
	wallpaper "system-stats/internal/platform/wallpaper"
	"system-stats/internal/webui"
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
		// ReportCaller (file:line per line) is useful when debugging but adds
		// a runtime.Caller cost and noise on every log — only when DEBUG is on.
		ReportCaller:    cfg.Debug,
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
	container, err := di.NewContainer(logger, cfg.Database, cfg.JWTSecret, cfg.RefreshSecret, startTime, cfg.Raft, cfg.TraefikDynamicDirs)
	if err != nil {
		logger.Fatal("Failed to initialize DI container", "error", err)
	}
	defer func() {
		if cerr := container.Close(); cerr != nil {
			logger.Warn("DI container close returned error", "error", cerr)
		}
	}()
	logger.Info("DI container initialized",
		"database_type", cfg.Database.Type,
		"raft_enabled", cfg.Raft.Enabled,
		"raft_cluster", cfg.Raft.ClusterID,
		"raft_node_id", cfg.Raft.NodeID,
	)

	regCtx, regCancel := context.WithTimeout(context.Background(), 30*time.Second)
	if _, err := container.GetHostService().RegisterOrUpdateCurrentHost(regCtx); err != nil {
		logger.Error("Failed to register local collector host (fixed host_id=1)", "error", err)
	}
	regCancel()

	// Raft-enabled deployments seed / read cluster-shared JWT signing keys
	// from cluster_config so a session minted on any node validates on
	// every other node. On the very first node these come from env; on
	// joiners they arrive via snapshot restore.
	if cfg.Raft.Enabled {
		bootCtx, bootCancel := context.WithTimeout(context.Background(), 15*time.Second)
		jwtSec, refSec, berr := raftcluster.BootstrapClusterSecrets(bootCtx,
			logger,
			container.GetRaftService(),
			container.GetRaftReplicator(),
			container.GetDB(),
			cfg.JWTSecret,
			cfg.RefreshSecret,
		)
		bootCancel()
		if berr != nil {
			logger.Warn("raft: cluster secret bootstrap returned error", "error", berr)
		}
		if jwtSec != cfg.JWTSecret || refSec != cfg.RefreshSecret {
			// We discovered cluster-shared secrets that differ from env;
			// rebuild the token service so newly minted sessions sign /
			// verify with the cluster-shared keys.
			cfg.JWTSecret = jwtSec
			cfg.RefreshSecret = refSec
			logger.Info("raft: token service will use cluster-shared signing keys (restart required for full effect)")
		}

		go func() {
			// Give the leader a moment to settle, then advertise this
			// node's HTTP URL into the catalog. Used by both the
			// cross-cluster bridge picker AND the follower→leader
			// SubmitCommand forwarder, which can't function without
			// every node's URL being discoverable.
			time.Sleep(2 * time.Second)
			advCtx, advCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer advCancel()
			adURL := cfg.Raft.AdvertiseURL
			if adURL == "" {
				adURL = deriveHTTPURL(cfg.Addr, cfg.Raft.AdvertiseAddr)
			}
			raftcluster.AdvertiseSelf(advCtx, logger,
				container.GetRaftService(),
				container.GetRaftReplicator(),
				cfg.Raft.ClusterID,
				cfg.Raft.NodeID,
				adURL,
			)
		}()
	}

	// appCtx is cancelled on shutdown to stop background goroutines
	// (periodic metrics collection, retention cleanup).
	appCtx, appCancel := context.WithCancel(context.Background())
	defer appCancel()

	// Hand appCtx to the DI container so wizard-driven hot-activations
	// can spawn bridge goroutines against it. Also starts the bridge
	// picker / sender immediately if Raft was activated at boot time
	// (i.e. RAFT_ENABLED=true in env).
	container.SetAppContext(appCtx)

	// Periodically hand freed heap memory back to the OS so the process RSS
	// tracks the live heap instead of drifting up to the GOMEMLIMIT high-water
	// mark (Go's MADV_FREE scavenge otherwise leaves freed pages mapped).
	runtimetune.StartPeriodicRelease(appCtx, 2*time.Minute)

	// Connectors: external data-source registry + the Proxmox poller. The
	// cipher key derives from the (cluster-shared) JWT secret so connector
	// credentials encrypted on one node decrypt on whichever node leads.
	connCipher := connectors.NewCipher(cfg.JWTSecret)
	connDetector := connectors.NewDetector(logger)
	// PEXELS_API_BASE overrides the upstream (tests / corporate proxies).
	pexelsClient := wallpaper.NewClient(os.Getenv("PEXELS_API_BASE"))
	connectorsSvc := connectors.NewService(
		logger,
		container.GetConnectorRepository(),
		container.GetHostRepository(),
		connDetector,
		connCipher,
		proxmox.NewProber(),
		pexelsClient,
		container.GetRaftReplicator(),
	)
	wallpaperSvc := wallpaper.NewService(logger, container.GetConnectorRepository(), connCipher, pexelsClient)
	pvePoller := proxmox.NewPoller(proxmox.PollerDeps{
		Logger:     logger,
		Connectors: container.GetConnectorRepository(),
		Cipher:     connCipher,
		HostRepo:   container.GetHostRepository(),
		Detector:   connDetector,
		Raft:       container.GetRaftReplicator(),
		RaftSvc:    container.GetRaftService(),
		CPURepo:    container.GetCPURepository(),
		MemRepo:    container.GetMemoryRepository(),
		DiskRepo:   container.GetDiskRepository(),
		NetRepo:    container.GetNetworkRepository(),
		Publish:    container.GetBroker().Publish,
		// Best-effort cluster fanout for guest metrics (off-Raft). Resolved at
		// call time since the metric sender is built on Raft activation, after
		// the poller is constructed.
		BroadcastMetrics: func(ctx context.Context, batch raftcluster.MetricBatchPayload) {
			if s := container.GetMetricSender(); s != nil {
				s.Broadcast(ctx, batch)
			}
		},
	})
	connectorsSvc.SetSyncTrigger(pvePoller.TriggerSync)
	go pvePoller.Run(appCtx)

	historicalMetricsService := container.GetHistoricalMetricsService()
	retentionSvc := retention.NewService(container.GetDB(), logger, cfg.RetentionDays, container.GetRefreshTokenRepository())

	// Wire SSE broker into the after-collect hook (harmless before collection starts).
	broker := container.GetBroker()
	systemSvc := container.GetSystemService()
	historicalMetricsService = history.WithAfterCollect(historicalMetricsService, func() {
		collectCtx, collectCancel := context.WithTimeout(appCtx, 10*time.Second)
		defer collectCancel()
		metrics, err := systemSvc.CollectAllCurrent(collectCtx)
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
		if host, herr := container.GetHostService().GetCurrentHost(appCtx); herr == nil && host != nil {
			envelope["collecting_host_id"] = host.ID
		}
		out, err := json.Marshal(envelope)
		if err != nil {
			return
		}
		var s struct {
			CPU struct {
				UsagePercent float64 `json:"usage_percent"`
			} `json:"cpu"`
			Memory struct {
				UsagePercent float64 `json:"usage_percent"`
			} `json:"memory"`
			Docker struct {
				RunningContainers int `json:"running_containers"`
			} `json:"docker"`
		}
		if err := json.Unmarshal(data, &s); err == nil {
			logger.Info("Metrics collected",
				"cpu", fmt.Sprintf("%.1f%%", s.CPU.UsagePercent),
				"mem", fmt.Sprintf("%.1f%%", s.Memory.UsagePercent),
				"containers", s.Docker.RunningContainers,
			)
		}
		broker.Publish(out)

		// Replicate this host's metrics to the cluster via the best-effort metric
		// stream (NOT the durable Raft log — that is what ballooned the log/RSS).
		// Each peer (and, when uplinking, the cross-cluster hub) receives the full
		// current state every tick, so a dropped batch self-heals next cycle. The
		// metrics survive this node going offline because peers persist them
		// locally. No-op when Raft is inactive (sender is nil).
		if sender := container.GetMetricSender(); sender != nil {
			if host, herr := container.GetHostService().GetCurrentHost(appCtx); herr == nil && host != nil && host.MacAddress != "" {
				batch := raftcluster.MetricBatchPayload{
					HostMAC:   host.MacAddress,
					HostName:  host.Name,
					Timestamp: time.Now().UTC(),
				}
				if v := metrics["cpu"]; v != nil {
					if b, e := json.Marshal(v); e == nil {
						batch.CPU = b
					}
				}
				if v := metrics["memory"]; v != nil {
					if b, e := json.Marshal(v); e == nil {
						batch.Memory = b
					}
				}
				if v := metrics["disk"]; v != nil {
					if b, e := json.Marshal(v); e == nil {
						batch.Disk = b
					}
				}
				if v := metrics["network"]; v != nil {
					if b, e := json.Marshal(v); e == nil {
						batch.Network = b
					}
				}
				if v := metrics["docker"]; v != nil {
					if b, e := json.Marshal(v); e == nil {
						batch.Docker = b
					}
				}
				// Broadcast fires one short-timeout POST per target and returns
				// immediately, so it never blocks the collection cycle.
				sender.Broadcast(appCtx, batch)
			}
		}

		// Run due retention chores off the metrics tick — each is self-throttled
		// (metrics ~2min, tokens ~10min), so most ticks are cheap no-ops. Bounded
		// by appCtx + a short deadline so it never blocks the collection cycle.
		go func() {
			cleanupCtx, cancel := context.WithTimeout(appCtx, 10*time.Second)
			defer cancel()
			retentionSvc.RunDue(cleanupCtx)
		}()
	})

	// startMetrics activates periodic collection and retention.
	// Called immediately on normal startup, or as a callback once setup completes.
	startMetrics := func() {
		logger.Info("Checking system stats availability...")
		if _, err := container.GetCPUService().Collect(appCtx); err != nil {
			logger.Warn("Failed to get initial CPU stats, continuing without check", "error", err)
		} else {
			logger.Info("System stats are available")
		}

		logger.Info("Starting periodic metrics collection...")
		if err := historicalMetricsService.StartPeriodicCollection(appCtx, cfg.MetricsInterval, cfg.MetricsPersistInterval); err != nil {
			logger.Error("Failed to start periodic collection", "error", err)
			return
		}
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
			go startMetrics()
		}
	} else {
		// Run startMetrics in a goroutine so the HTTP server can start
		// accepting connections immediately instead of blocking on the first
		// metrics collection cycle (CPU sampling alone takes ~1 s).
		go startMetrics()
	}

	// Whenever Raft activates (boot-time env, wizard "Start new cluster"
	// or wizard "Join existing cluster"), ensure metrics collection is
	// running. The historicalMetricsService.Start is idempotent — calling
	// it on an already-running loop is a no-op. Without this, a wizard
	// JoinRaftCluster flow would leave the node without metrics: it
	// activates Raft + replicates state but never fires the
	// /setup/complete onSetupComplete callback, so the local collector
	// (which is also what publishes this node's host info into Raft via
	// SubmitHostUpsert) never runs and the leader's dashboard never
	// shows the new node.
	container.SetPostActivateHook(func() {
		logger.Info("Raft activated — ensuring metrics collection is running")
		go startMetrics()
	})

	// If Raft was already activated at boot time (RAFT_ENABLED=true in
	// .env from a previous wizard run), the post-activate hook above
	// fires too late — it's a no-op for the boot-time activation that
	// already happened inside NewContainer. Bridge metrics-start here
	// explicitly so a fresh restart of a configured node doesn't sit
	// without metrics until the next wizard interaction.
	if container.GetRaftService() != nil && container.GetRaftService().Enabled() {
		go startMetrics()
	}

	router := setupRouter(container, startTime, logger, cfg, onSetupComplete, connectorsSvc, wallpaperSvc)

	server := &http.Server{
		Addr:         cfg.Addr,
		Handler:      router,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	serverErr := make(chan error, 1)
	go func() {
		logger.Info("Starting server", "address", cfg.Addr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("Server failed to listen", "error", err)
			serverErr <- err
		}
	}()

	select {
	case <-quit:
		logger.Info("Received interrupt signal, shutting down gracefully...")
	case err := <-serverErr:
		logger.Error("Server exited unexpectedly", "error", err)
	}

	historicalMetricsService.StopPeriodicCollection()
	appCancel()
	middleware.StopRateLimiterCleanup()

	// Unblock all SSE clients (live + keepalive loops) so they don't hold the
	// server open, and stop accepting new keep-alive connections, before the
	// graceful drain.
	broker.CloseAll()
	server.SetKeepAlivesEnabled(false)

	// 8s drain so the worst case still fits Docker's default 10s stop grace
	// period (SIGTERM → SIGKILL) and we exit cleanly instead of being killed.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error("Server forced to shutdown", "error", err)
	} else {
		logger.Info("Server exited gracefully")
	}
}

// setupRouter configures the Gin router with all routes, middleware, and handlers.
func setupRouter(container *di.Container, startTime time.Time, logger *log.Logger, cfg *config.Config, onSetupComplete func(), connectorsSvc connectors.Service, wallpaperSvc *wallpaper.Service) *gin.Engine {
	router := gin.New()
	// TrustedProxies controls whether X-Forwarded-* headers are honored.
	// Empty list = trust none (ignore X-Forwarded-For/Host/Proto). Safe default.
	if len(cfg.TrustedProxies) > 0 {
		_ = router.SetTrustedProxies(cfg.TrustedProxies)
	} else {
		_ = router.SetTrustedProxies(nil)
	}
	router.Use(gin.Recovery())
	router.Use(middleware.SecurityHeaders(cfg.CookieSecure))
	router.Use(middleware.ErrorHandler(cfg.Debug))
	// gzip compresses JSON history payloads; SSE and Prometheus paths must NOT be buffered.
	router.Use(gzip.Gzip(
		gzip.DefaultCompression,
		gzip.WithExcludedPaths([]string{"/api/v1/stream", "/api/v1/metrics"}),
	))

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

	// Frontend: prefer the embedded build (release binaries built with
	// -tags embed_dist), falling back to on-disk internal/webui/dist for dev.
	// In dev the Vite dev-server proxies /api and serves the SPA itself, so the
	// on-disk fallback is only hit when someone runs the plain binary after a
	// `yarn build`.
	var staticFS fs.FS
	if efs := webui.FS(); efs != nil {
		staticFS = efs
		logger.Info("Serving embedded frontend")
	} else {
		wd, err := os.Getwd()
		if err != nil {
			logger.Fatal("Failed to get working directory", "error", err)
		}
		distPath := filepath.Join(wd, "internal", "webui", "dist")
		staticFS = os.DirFS(distPath)
		logger.Info("Serving static files from disk", "path", distPath)
	}

	systemHandler := system.NewHandler(logger, container.GetSystemService(), container.GetHostService())
	cpuHandler := cpu.NewHandler(logger, container.GetCPUService(), container.GetHostService())
	memoryHandler := memory.NewHandler(logger, container.GetMemoryService(), container.GetHostService())
	diskHandler := disk.NewHandler(logger, container.GetDiskService(), container.GetHostService())
	networkHandler := network.NewHandler(logger, container.GetNetworkService(), container.GetHostService())
	dockerHandler := docker.NewHandler(logger, container.GetDockerService(), container.GetHostService())
	sensorsHandler := sensors.NewHandler(logger, container.GetSensorsService(), container.GetHostService())
	hostHandler := hosts.NewHandler(logger, container.GetHostService())
	connectorsHandler := connectors.NewHandler(logger, connectorsSvc)
	wallpaperHandler := wallpaper.NewHandler(logger, wallpaperSvc)
	healthHandler := health.NewHandler(logger, container.GetHealthService())
	authHandler := users.NewAuthHandler(container.GetUserService(), container.GetTokenService(), cfg.CookieSecure)
	usersHandler := users.NewUsersHandler(container.GetUserService())
	invitationHandler := invitations.NewHandler(container.GetInvitationService())
	streamHandler := platformstream.NewHandler(container.GetBroker(), container.GetHostService())
	configWriter := setup.NewConfigWriter()

	// Update service: polls GitHub Releases, exposes /version, drives the
	// auto-update toggle + "update now" (docker → controller; native → self-replace).
	updateDataDir := os.Getenv("NODE_STATS_DATA_DIR")
	if updateDataDir == "" {
		updateDataDir = "/app/data"
	}
	updateSvc := update.NewService(
		os.Getenv("NODE_STATS_REPO"),
		updateDataDir,
		strings.EqualFold(os.Getenv("AUTO_UPDATE"), "true"),
		func(enabled bool) error {
			cv, err := configWriter.ReadCurrentConfig()
			if err != nil {
				return err
			}
			if enabled {
				cv.AutoUpdate = "true"
			} else {
				cv.AutoUpdate = "false"
			}
			if err := configWriter.WriteConfigFile(cv); err != nil {
				return err
			}
			// Keep the process env in sync: ReadCurrentConfig merges from
			// os.Getenv (godotenv.Load never overrides set vars), so a later
			// .env rewrite would otherwise resurrect the boot-time value.
			return os.Setenv("AUTO_UPDATE", cv.AutoUpdate)
		},
		func() (string, string) { return string(cfg.Database.Type), cfg.Database.DSN },
	).WithDeployWebhook(
		os.Getenv("NODE_STATS_DEPLOY_WEBHOOK_URL"),
		func(url string) error {
			cv, err := configWriter.ReadCurrentConfig()
			if err != nil {
				return err
			}
			cv.DeployWebhookURL = url
			if err := configWriter.WriteConfigFile(cv); err != nil {
				return err
			}
			return os.Setenv("NODE_STATS_DEPLOY_WEBHOOK_URL", url)
		},
	)
	updateSvc.Start(context.Background())

	setupHandler := setup.NewHandler(configWriter, container.GetUserService(), onSetupComplete).
		WithRaft(container.GetRaftService()).
		WithRaftActivator(container).
		WithTokenService(container.GetTokenService()).
		WithSecretReader(&clusterSecretAdapter{db: container.GetDB()}).
		WithHTTPAddr(cfg.Addr)
	raftHandler := raftcluster.NewHandler(container.GetRaftService()).
		WithDeps(container.GetRaftReplicator(), container.GetDB(), logger, cfg.Raft.ClusterID).
		WithBridgeConfigurator(container).
		WithBootError(container.RaftBootError).
		WithResetConfig(container.ResetRaftConfig).
		WithWipeState(container.WipeRaftState).
		WithFactoryReset(container.FactoryResetRaft).
		WithPickerInfo(func() any {
			if p := container.GetBridgePicker(); p != nil {
				return p.Snapshot()
			}
			return nil
		}).
		WithBridgeInfo(container.BridgeInfo)

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

		// Public: build/version + update info (no auth) — consumed by the update
		// banner and the wizard's post-restart readiness gate. ?refresh=1 forces a
		// release re-check (rate-limited service-side) so the update popup shows
		// fresh state instead of the hourly cache.
		api.GET("/version", func(c *gin.Context) {
			if c.Query("refresh") != "" {
				updateSvc.RefreshIfStale(c.Request.Context(), time.Minute)
			}
			c.JSON(http.StatusOK, gin.H{"data": updateSvc.Status()})
		})

		// Setup routes (public, only work when no users exist)
		setup := api.Group("/setup")
		{
			setup.GET("/status", setupHandler.Status)
			setup.GET("/config", setupHandler.GetConfig)
			setup.POST("/preview-env", setupHandler.PreviewEnv)
			setup.POST("/complete", setupHandler.CompleteSetup)
			setup.POST("/join-raft-cluster", setupHandler.JoinRaftCluster)
			setup.POST("/check-reachable", setupHandler.CheckReachable)
			setup.POST("/db/test", setupHandler.TestDB)
			setup.GET("/raft-progress", setupHandler.RaftProgress)
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
			users.POST("/me/password", usersHandler.ChangeOwnPassword)
			users.GET("", middleware.RequireAdmin(), usersHandler.List)
			users.PATCH("/:id", middleware.RequireAdmin(), usersHandler.UpdateRole)
			users.POST("/:id/reset-password", middleware.RequireAdmin(), usersHandler.ResetUserPassword)
			users.DELETE("/:id", middleware.RequireAdmin(), usersHandler.Delete)
		}

		// Invitations (admin only)
		invitations := api.Group("/invitations", middleware.AuthJWT(container.GetTokenService()), middleware.RequireAdmin())
		{
			invitations.POST("", invitationHandler.CreateInvitation)
		}

		// Raft ping is public on purpose: peer clusters need it to measure
		// round-trip latency without sharing user credentials. The mutating
		// bridge endpoints (added in a follow-up) live behind HMAC auth.
		api.GET("/raft/ping", raftHandler.Ping)

		// Raft join: a fresh node uses a one-shot token (issued by an
		// admin on the leader) to enrol itself as a voter. Rate-limited
		// to stop token-bruteforce attempts.
		raftJoinRL := middleware.RateLimitMiddleware(5.0/60, 5)
		api.POST("/raft/join", raftJoinRL, raftHandler.Join)

		// Follower → leader command forwarding. Public because the
		// payload only mutates already-shared cluster state and the
		// leader's SubmitCommand validates it. The limit is per client IP
		// (i.e. per peer node) and must comfortably absorb steady cluster
		// traffic: every follower forwards its host upsert + metric batch
		// each ~5s cycle, plus a burst of backfill right after joining.
		// 50/s (burst 100) per node leaves ample headroom while still
		// capping a single misbehaving peer.
		raftForwardRL := middleware.RateLimitMiddleware(50, 100)
		api.POST("/raft/forward", raftForwardRL, raftHandler.Forward)

		// Cross-cluster bridge receiver — HMAC-authenticated by the
		// handler itself, so no JWT middleware here. Resolved per request:
		// the receiver may be (re)built at runtime when the admin enables
		// uplink receiving, long after the routes were registered.
		api.POST("/raft/bridge/replicate", func(c *gin.Context) {
			if br := container.GetBridgeReceiver(); br != nil {
				br.Handle(c)
				return
			}
			c.JSON(http.StatusNotFound, gin.H{"error": "bridge receiving is not enabled on this node"})
		})

		// Best-effort metric stream (off-Raft): peers POST their host metrics
		// here; we authenticate (HMAC) and write them straight into local state +
		// SSE without a consensus round. Resolved at request time since the
		// receiver is (re)built on every Raft activation.
		api.POST(metricstream.RoutePath, func(c *gin.Context) {
			if mr := container.GetMetricReceiver(); mr != nil {
				mr.Handle(c)
				return
			}
			c.JSON(http.StatusNotFound, gin.H{"error": "metric stream not enabled on this node"})
		})

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
		authAPI.GET("/docker/traefik-discovery", dockerHandler.HandleTraefikDiscovery)
		authAPI.GET("/applications", dockerHandler.HandleApplications)
		authAPI.GET("/applications/:project", dockerHandler.HandleApplicationDetail)
		authAPI.GET("/applications/:project/compose", dockerHandler.HandleApplicationCompose)
		authAPI.GET("/applications/:project/logs", dockerHandler.HandleApplicationLogs)
		authAPI.GET("/sensors", sensorsHandler.HandleSensors)
		authAPI.GET("/hosts", hostHandler.HandleGetAllHosts)
		authAPI.GET("/hosts/current", hostHandler.HandleGetCurrentHost)
		authAPI.POST("/hosts/register", hostHandler.HandleRegisterCurrentHost)
		// Remove a registered host + its metrics (admin; cluster-wide when Raft on)
		authAPI.DELETE("/hosts/:id", middleware.RequireAdmin(), hostHandler.HandleDeleteHost)
		authAPI.GET("/stream", streamHandler.HandleStream)

		// Connectors (admin): environment-detection hints + external data
		// sources (Proxmox). Replicated cluster-wide when Raft is on.
		authAPI.GET("/connectors", middleware.RequireAdmin(), connectorsHandler.HandleList)
		authAPI.POST("/connectors", middleware.RequireAdmin(), connectorsHandler.HandleCreateProxmox)
		authAPI.POST("/connectors/proxmox/test", middleware.RequireAdmin(), connectorsHandler.HandleTestProxmox)
		authAPI.POST("/connectors/pexels", middleware.RequireAdmin(), connectorsHandler.HandleSavePexels)
		authAPI.PATCH("/connectors/:id", middleware.RequireAdmin(), connectorsHandler.HandleUpdate)
		authAPI.DELETE("/connectors/:id", middleware.RequireAdmin(), connectorsHandler.HandleDelete)
		authAPI.POST("/connectors/:id/sync", middleware.RequireAdmin(), connectorsHandler.HandleSync)

		// Dynamic wallpaper (any signed-in user): proxies the Pexels connector
		// so the API key never reaches the browser.
		authAPI.GET("/wallpaper", wallpaperHandler.HandleCurrent)

		// Application icons (any signed-in user): server-side resolution
		// against icon registries (selfh.st index collapsed-name matching)
		// with an in-memory cache shared by every client.
		appIconsSvc := appicons.NewService(logger)
		authAPI.GET("/app-icons/:slug", appIconsSvc.Handle)

		// Raft cluster status (admin) — surfaces leader, peers, indices, RTTs
		authAPI.GET("/raft/status", middleware.RequireAdmin(), raftHandler.Status)
		// Raft peer management (admin, leader-only)
		authAPI.POST("/raft/peers", middleware.RequireAdmin(), raftHandler.AddPeer)
		authAPI.DELETE("/raft/peers/:id", middleware.RequireAdmin(), raftHandler.RemovePeer)
		// Issue one-shot join token (admin, leader-only)
		authAPI.POST("/raft/join-token", middleware.RequireAdmin(), raftHandler.IssueJoinToken)
		// Hot-update cross-cluster bridge configuration
		authAPI.POST("/raft/bridge", middleware.RequireAdmin(), raftHandler.SaveBridgeConfig)
		// Read back the configured uplink secret (for enrolling more sites)
		authAPI.GET("/raft/bridge/secret", middleware.RequireAdmin(), raftHandler.GetBridgeSecret)
		// Read back the full uplink configuration (form prefill)
		authAPI.GET("/raft/bridge", middleware.RequireAdmin(), raftHandler.GetBridgeConfig)
		// Wipe RAFT_* from .env so the next restart boots Raft-disabled
		authAPI.POST("/raft/reset", middleware.RequireAdmin(), raftHandler.ResetConfig)
		// Wipe Raft on-disk state + re-bootstrap as fresh single voter
		authAPI.POST("/raft/wipe-state", middleware.RequireAdmin(), raftHandler.WipeState)
		// Fully decouple from Raft: wipe state + remove .env entries
		authAPI.POST("/raft/factory-reset", middleware.RequireAdmin(), raftHandler.FactoryReset)
		// Self-leave: remove THIS node from the cluster, then decouple to standalone
		authAPI.POST("/raft/leave", middleware.RequireAdmin(), raftHandler.Leave)
		// TCP-probe a voter's advertise addr from this server
		authAPI.POST("/raft/probe-voter", middleware.RequireAdmin(), raftHandler.ProbeVoter)

		// Form / join a cluster at runtime from an already-provisioned node
		// (admin). "start" bootstraps this node as the leader (non-destructive);
		// "join" attaches it to an existing cluster (replaces local data — the
		// handler requires an explicit acknowledge_data_loss flag).
		authAPI.POST("/cluster/start", middleware.RequireAdmin(), setupHandler.AdminStartCluster)
		authAPI.POST("/cluster/join", middleware.RequireAdmin(), setupHandler.AdminJoinCluster)
		// Pre-fills the cluster-sync forms: detected LAN IP + TCP-probed
		// advertise-URL candidates (incl. the browser origin via ?origin=).
		authAPI.GET("/cluster/advertise-hints", middleware.RequireAdmin(), setupHandler.AdvertiseHints)

		// Auto-update settings (admin). Toggle persists to .env.agent; "now"
		// applies immediately (docker → controller pull+recreate; native → self-replace).
		authAPI.POST("/settings/auto-update", middleware.RequireAdmin(), func(c *gin.Context) {
			var body struct {
				Enabled bool `json:"enabled"`
			}
			if err := c.ShouldBindJSON(&body); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"code": "validation_error", "error": "Invalid request data"})
				return
			}
			if err := updateSvc.SetAutoUpdate(c.Request.Context(), body.Enabled); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"code": "internal_error", "error": err.Error()})
				return
			}
			c.JSON(http.StatusOK, gin.H{"data": updateSvc.Status()})
		})
		authAPI.POST("/settings/update-now", middleware.RequireAdmin(), func(c *gin.Context) {
			msg, err := updateSvc.UpdateNow(c.Request.Context())
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"code": "update_failed", "error": err.Error()})
				return
			}
			c.JSON(http.StatusOK, gin.H{"data": gin.H{"message": msg}})
		})
		// Orchestrator deploy-webhook (admin): lets a managed-externally
		// deployment (dokploy, …) update itself — update-now/auto-update POST
		// the orchestrator's own deploy URL instead of asking the controller.
		// The URL embeds a deploy token, hence admin-only and absent from /version.
		authAPI.GET("/settings/deploy-webhook", middleware.RequireAdmin(), func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{"data": gin.H{"url": updateSvc.DeployWebhookURL()}})
		})
		authAPI.POST("/settings/deploy-webhook", middleware.RequireAdmin(), func(c *gin.Context) {
			var body struct {
				URL string `json:"url"`
			}
			if err := c.ShouldBindJSON(&body); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"code": "validation_error", "error": "Invalid request data"})
				return
			}
			if err := updateSvc.SetDeployWebhook(body.URL); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"code": "validation_error", "error": err.Error()})
				return
			}
			c.JSON(http.StatusOK, gin.H{"data": updateSvc.Status()})
		})
	}

	// Static files for React app (hashed bundles from Vite). Served from the
	// embedded or on-disk fs; a missing assets subtree just 404s (dev/Vite case).
	if assetsFS, err := fs.Sub(staticFS, "assets"); err == nil {
		router.StaticFS("/assets", http.FS(assetsFS))
	}

	// SPA fallback routing
	router.NoRoute(func(c *gin.Context) {
		if strings.HasPrefix(c.Request.URL.Path, "/api/") {
			c.JSON(404, gin.H{"error": "API endpoint not found"})
			return
		}
		m := c.Request.Method
		if m == http.MethodGet || m == http.MethodHead {
			if name, ok := resolveFSStaticFile(staticFS, c.Request.URL.Path); ok {
				http.ServeFileFS(c.Writer, c.Request, staticFS, name)
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
		http.ServeFileFS(c.Writer, c.Request, staticFS, "index.html")
	})

	return router
}

// resolveFSStaticFile validates urlPath against the static fs and returns a
// slash-cleaned, embed-safe name. fs.ValidPath is the canonical traversal
// guard for io/fs (rejects "..", absolute paths, and empty segments).
func resolveFSStaticFile(fsys fs.FS, urlPath string) (name string, ok bool) {
	rel := strings.TrimPrefix(urlPath, "/")
	if rel == "" {
		return "", false
	}
	rel = path.Clean(rel)
	if !fs.ValidPath(rel) {
		return "", false
	}
	info, err := fs.Stat(fsys, rel)
	if err != nil || info.IsDir() {
		return "", false
	}
	return rel, true
}

// firstNonEmpty returns a if it's non-empty, else b. Used to fall back from
// RAFT_ADVERTISE_ADDR to RAFT_BIND_ADDR.
func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// deriveHTTPURL constructs a fallback HTTP advertise URL from the
// listening ADDR (":8080") and the Raft advertise address (the host
// portion). Used when RAFT_ADVERTISE_PUBLIC_URL is unset — picks the
// IP/hostname the operator already provided as Raft's advertise and
// pairs it with the HTTP port from cfg.Addr. Returns "" if neither
// fits.
func deriveHTTPURL(addr, raftAdvertise string) string {
	host := ""
	if h, _, err := net.SplitHostPort(raftAdvertise); err == nil && h != "" {
		host = h
	}
	if host == "" {
		// Try OS hostname as last resort; "localhost" is useless for
		// peers but better than a blank URL.
		if hn, err := os.Hostname(); err == nil {
			host = hn
		} else {
			host = "localhost"
		}
	}
	_, port, err := net.SplitHostPort(addr)
	if err != nil || port == "" {
		port = "8080"
	}
	return "http://" + net.JoinHostPort(host, port)
}

// clusterSecretAdapter adapts the raft package's LookupClusterConfig
// function to the small ClusterSecretReader interface the setup handler
// expects.
type clusterSecretAdapter struct {
	db *gorm.DB
}

// LookupClusterSecret reads a single key from the replicated
// cluster_config table.
func (a *clusterSecretAdapter) LookupClusterSecret(ctx context.Context, key string) (string, error) {
	if a.db == nil {
		return "", nil
	}
	return raftcluster.LookupClusterConfig(ctx, a.db, key)
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
