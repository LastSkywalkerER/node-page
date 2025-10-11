/**
 * Package server provides the HTTP server implementation for the system statistics API.
 * This package handles HTTP request routing, middleware setup, and graceful shutdown
 * for the system monitoring application, serving both REST API endpoints and static files.
 */
package server

import (
	"context"
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

	"system-stats/internal/app/di"
	"system-stats/internal/app/middleware"
	cpumodule "system-stats/internal/modules/cpu/presentation"
	diskmodule "system-stats/internal/modules/disk/presentation"
	dockermodule "system-stats/internal/modules/docker/presentation"
	healthmodule "system-stats/internal/modules/health/presentation"
	hostmodule "system-stats/internal/modules/hosts/presentation"
	memorymodule "system-stats/internal/modules/memory/presentation"
	networkmodule "system-stats/internal/modules/network/presentation"
	sensorsmodule "system-stats/internal/modules/sensors/presentation"
	systemmodule "system-stats/internal/modules/system/presentation"
)

/**
 * Run starts the system statistics HTTP server.
 * This function initializes the dependency injection container, sets up periodic
 * metrics collection, configures HTTP routes, and handles graceful shutdown.
 * The server provides REST API endpoints for metrics data and serves the React dashboard.
 */
func Run() {
	// Parse command line arguments for server configuration
	/** addr specifies the HTTP server listening address (host:port format) */
	var (
		addr = flag.String("addr", ":8080", "HTTP server address")
		/** help flag triggers display of command-line help and exits */
		help = flag.Bool("help", false, "Show help message")
		/** dbPath specifies the file path to the SQLite database file */
		dbPath = flag.String("db", "stats.db", "SQLite database path")
		/** mode sets Gin framework mode: "debug" for development or "release" for production */
		mode = flag.String("mode", "release", "Gin mode (debug/release)")
		/** debug enables debug logging level (default: info, warn, error) */
		debug = flag.Bool("debug", false, "Enable debug logging")
	)
	flag.Parse()

	// Show help message and exit if requested
	if *help {
		printHelp()
		return
	}

	// Initialize logger with debug level if requested
	logger := log.NewWithOptions(os.Stderr, log.Options{
		ReportCaller:    true,
		ReportTimestamp: true,
		Prefix:          "system-stats",
	})
	log.SetDefault(logger)
	if *debug {
		log.SetLevel(log.DebugLevel)
	} else {
		log.SetLevel(log.InfoLevel)
	}

	// Configure Gin framework mode (debug for development, release for production)
	if *mode == "release" {
		gin.SetMode(gin.ReleaseMode)
	}

	// Record server start time for uptime calculations
	/** startTime records when the server was started for health check uptime calculation */
	startTime := time.Now()

	// Initialize dependency injection container with database configuration
	logger.Info("Initializing dependency injection container...")
	/** container holds all application dependencies and services configured via dependency injection */
	container, err := di.NewContainer(logger, *dbPath, startTime)
	if err != nil {
		logger.Fatal("Failed to initialize DI container", "error", err)
	}
	logger.Info("DI container initialized", "database", *dbPath)

	historicalMetricsService := container.GetHistoricalMetricsService()

	// Verify that system metrics collection is working properly
	logger.Info("Checking system stats availability...")
	_, err = container.GetCPUService().Collect(context.Background())
	if err != nil {
		logger.Warn("Failed to get initial CPU stats, continuing without check", "error", err)
	} else {
		logger.Info("System stats are available")
	}

	// Start background periodic metrics collection (every 5 seconds)
	logger.Info("Starting periodic metrics collection...")
	err = historicalMetricsService.StartPeriodicCollection(context.Background(), 5*time.Second)
	if err != nil {
		logger.Fatal("Failed to start periodic collection", "error", err)
	}

	// Create and configure Gin router with all routes and middleware
	/** router is the configured Gin HTTP router with all API routes and middleware */
	router := setupRouter(container, startTime, logger)

	// Create HTTP server with configured router
	/** server is the HTTP server instance that will handle incoming requests */
	server := &http.Server{
		Addr:    *addr,
		Handler: router,
	}

	// Set up signal handling for graceful shutdown
	/** quit is a channel that receives OS signals for graceful server shutdown */
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	// Start HTTP server in background goroutine
	go func() {
		logger.Info("Attempting to start server", "address", *addr)
		if err := server.ListenAndServe(); err != nil {
			logger.Error("Server error", "error", err)
		}
		logger.Info("Server is running", "address", *addr)
	}()

	// Wait for shutdown signal
	<-quit
	logger.Info("Received interrupt signal, shutting down gracefully...")

	// Stop periodic metrics collection before shutting down
	historicalMetricsService.StopPeriodicCollection()

	// Attempt graceful shutdown with timeout
	if err := server.Shutdown(context.Background()); err != nil {
		logger.Error("Server forced to shutdown", "error", err)
	} else {
		logger.Info("Server exited gracefully")
	}
}

/**
 * setupRouter configures the Gin router with all routes, middleware, and handlers.
 * This function sets up API endpoints, static file serving, and SPA routing for the React dashboard.
 *
 * @param container The dependency injection container with all required services
 * @param startTime The application start time for calculating uptime responses
 * @param logger The logger instance for logging
 * @return *gin.Engine The configured Gin router instance
 */
func setupRouter(container *di.Container, startTime time.Time, logger *log.Logger) *gin.Engine {
	/** router is the main Gin HTTP router instance for handling all API and static file requests */
	router := gin.New()

	// Add essential middleware for error recovery, logging, and CORS
	/** gin.Recovery() provides panic recovery middleware to prevent server crashes */
	router.Use(gin.Recovery())

	/** middleware.LoggingMiddleware() logs all HTTP requests with method, URI, status, duration and client info */
	router.Use(middleware.LoggingMiddleware(logger))

	/** middleware.CORSMiddleware() handles Cross-Origin Resource Sharing headers for web clients */
	router.Use(middleware.CORSMiddleware())

	// Determine the path to the built React application static files
	/** wd represents the current working directory path where the application is running */
	wd, err := os.Getwd()
	if err != nil {
		logger.Fatal("Failed to get working directory", "error", err)
	}

	/** distPath is the absolute path to the directory containing built React application files */
	distPath := filepath.Join(wd, "dist")
	logger.Info("Serving static files", "path", distPath)

	// Create handlers with all required dependencies from DI container

	/** systemHandler handles HTTP requests for system metrics dashboard */
	systemHandler := systemmodule.NewSystemHandler(logger, container.GetSystemService())

	/** cpuHandler handles HTTP requests for CPU metrics */
	cpuHandler := cpumodule.NewCPUHandler(logger, container.GetCPUService())

	/** memoryHandler handles HTTP requests for memory metrics */
	memoryHandler := memorymodule.NewMemoryHandler(logger, container.GetMemoryService())

	/** diskHandler handles HTTP requests for disk metrics */
	diskHandler := diskmodule.NewDiskHandler(logger, container.GetDiskService())

	/** networkHandler handles HTTP requests for network metrics */
	networkHandler := networkmodule.NewNetworkHandler(logger, container.GetNetworkService())

	/** dockerHandler handles HTTP requests for docker container metrics */
	dockerHandler := dockermodule.NewDockerHandler(logger, container.GetDockerService())

	/** sensorsHandler handles HTTP requests for sensors (temperatures) */
	sensorsHandler := sensorsmodule.NewSensorsHandler(logger, container.GetSensorsService())

	/** hostHandler handles HTTP requests for host information */
	hostHandler := hostmodule.NewHostHandler(logger, container.GetHostService())

	/** healthHandler handles HTTP requests for health checks */
	healthHandler := healthmodule.NewHealthHandler(logger, container.GetHealthService())

	// API routes for React dashboard - JSON endpoints for real-time data
	/** api is the route group for dashboard API endpoints that return JSON data */
	api := router.Group("/api")
	{
		/** GET /api/metrics/current - Returns current system metrics in JSON format for dashboard display */
		api.GET("/metrics/current", systemHandler.HandleCurrentMetrics)

		/** GET /api/metrics/historical - Historical metrics are accessed via individual module endpoints */

		/** GET /api/health - Health check endpoint for monitoring system status */
		api.GET("/health", healthHandler.HandleHealth)
	}

	// Stats routes for individual metrics - detailed JSON responses for each metric type
	{
		/** GET /api/cpu - Returns detailed CPU metrics including usage, cores, and load averages */
		api.GET("/cpu", cpuHandler.HandleCPUStats)

		/** GET /api/memory - Returns memory usage statistics including RAM and swap information */
		api.GET("/memory", memoryHandler.HandleMemoryStats)

		/** GET /api/disk - Returns disk storage metrics including usage percentages and space information */
		api.GET("/disk", diskHandler.HandleDiskStats)

		/** GET /api/network - Returns network interface statistics and traffic data */
		api.GET("/network", networkHandler.HandleNetworkStats)

		/** GET /api/docker - Returns docker container statistics and status information */
		api.GET("/docker", dockerHandler.HandleDockerStats)

		/** GET /api/sensors - Returns temperature sensors readings */
		api.GET("/sensors", sensorsHandler.HandleSensors)

		/** GET /api/hosts - Returns all registered hosts */
		api.GET("/hosts", hostHandler.HandleGetAllHosts)

		/** GET /api/hosts/current - Returns current host information */
		api.GET("/hosts/current", hostHandler.HandleGetCurrentHost)

		/** POST /api/hosts/register - Registers or updates current host */
		api.POST("/hosts/register", hostHandler.HandleRegisterCurrentHost)
	}

	// Static files for React app (assets, js, css) - serve built frontend assets
	router.Static("/assets", filepath.Join(distPath, "assets"))

	// SPA fallback routing - this must be last to handle client-side routing
	router.NoRoute(func(c *gin.Context) {
		// Return JSON errors for API routes that don't exist
		if strings.HasPrefix(c.Request.URL.Path, "/api/") {
			c.JSON(404, gin.H{"error": "API endpoint not found"})
			return
		}

		// Return 404 for asset routes that should have been handled above
		if strings.HasPrefix(c.Request.URL.Path, "/assets/") {
			c.Status(404)
			return
		}

		// Serve React app for all other routes (SPA fallback to index.html)
		c.File(filepath.Join(distPath, "index.html"))
	})

	return router
}

/**
 * printHelp displays the command-line help message with usage instructions and API documentation.
 * This function is called when the user provides the -help flag and shows all available
 * command-line options, API endpoints, and usage examples.
 */
func printHelp() {
	fmt.Println(`System Stats API Server

Usage:
  system-stats [options]

Options:
  -addr string    HTTP server address (default ":8080")
  -debug         Enable debug logging (default: info, warn, error)
  -help          Show this help message
  -db string     SQLite database path (default "stats.db")
  -mode string   Gin mode (debug/release) (default "release")

API Endpoints:
  GET /              - API documentation page
  GET /dashboard     - Beautiful dashboard with real-time charts
  GET /api/cpu     - CPU statistics (JSON)
  GET /api/memory  - Memory statistics (JSON)
  GET /api/disk    - Disk statistics (JSON)
  GET /api/network - Network statistics (JSON)
  GET /api/docker  - Docker containers statistics (JSON)
  GET /api/hosts   - All registered hosts (JSON)
  GET /api/hosts/current - Current host information (JSON)
  POST /api/hosts/register - Register/update current host
  GET /api/metrics/current - Current metrics for dashboard (JSON)
  GET /api/metrics/historical - Historical metrics for dashboard (JSON)
  GET /api/health    - Health check (JSON)

Examples:
  system-stats                    # Start server on :8080
  system-stats -addr :3000        # Start server on :3000

API usage examples:
  curl http://localhost:8080/api/cpu
  curl http://localhost:8080/api/memory
  curl http://localhost:8080/api/disk
  curl http://localhost:8080/api/network
  curl http://localhost:8080/api/docker
  curl http://localhost:8080/api/hosts
  curl http://localhost:8080/api/hosts/current
  curl -X POST http://localhost:8080/api/hosts/register
  curl http://localhost:8080/api/health
  curl "http://localhost:8080/api/health?host_id=1"`)
}
