/**
 * Package di provides dependency injection container for managing application dependencies.
 * This package implements the dependency injection pattern to wire together all components
 * of the system statistics application, ensuring proper initialization and lifecycle management.
 */
package di

import (
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	cpuservice "system-stats/internal/modules/cpu/application"
	cpuentities "system-stats/internal/modules/cpu/infrastructure/entities"
	cpurepos "system-stats/internal/modules/cpu/infrastructure/repositories"
	diskservice "system-stats/internal/modules/disk/application"
	diskentities "system-stats/internal/modules/disk/infrastructure/entities"
	diskrepos "system-stats/internal/modules/disk/infrastructure/repositories"
	dockerservice "system-stats/internal/modules/docker/application"
	dockerdomain "system-stats/internal/modules/docker/domain/repositories"
	dockercollectors "system-stats/internal/modules/docker/infrastructure/collectors"
	dockerrepos "system-stats/internal/modules/docker/infrastructure/repositories"
	historyapp "system-stats/internal/modules/history_metrics/application"
	historycore "system-stats/internal/modules/history_metrics/core"
	memoryservice "system-stats/internal/modules/memory/application"
	memoryentities "system-stats/internal/modules/memory/infrastructure/entities"
	memoryrepos "system-stats/internal/modules/memory/infrastructure/repositories"
	networkservice "system-stats/internal/modules/network/application"
	networkentities "system-stats/internal/modules/network/infrastructure/entities"
	networkrepos "system-stats/internal/modules/network/infrastructure/repositories"
	systemsrv "system-stats/internal/modules/system/application"

	"github.com/charmbracelet/log"
)

/**
 * Container represents the dependency injection container that holds all application dependencies.
 * This struct manages the lifecycle of all services, repositories, handlers, and infrastructure
 * components, providing getter methods for accessing initialized instances.
 */
type Container struct {
	/** logger provides structured logging throughout the application */
	logger *log.Logger

	/** repositories for each metric type */
	cpuRepository     cpurepos.CPURepository
	memoryRepository  memoryrepos.MemoryRepository
	diskRepository    diskrepos.DiskRepository
	networkRepository networkrepos.NetworkRepository
	dockerRepository  dockerdomain.DockerRepository

	/** individual services for each metric type */
	cpuService     cpuservice.Service
	memoryService  memoryservice.Service
	diskService    diskservice.Service
	networkService networkservice.Service
	dockerService  dockerservice.Service

	/** systemService provides aggregated system metrics */
	systemService systemsrv.Service

	/** historicalMetricsService manages historical metrics collection and storage */
	historicalMetricsService historycore.HistoricalMetricsService
}

/**
 * NewContainer creates a new dependency injection container with all application dependencies.
 * This constructor initializes the database, creates all repositories, services, collectors,
 * cache instances, and command/query handlers in the correct dependency order.
 *
 * @param logger The logger instance for structured logging
 * @param dbPath The file path to the SQLite database
 * @return *Container The initialized dependency injection container
 * @return error Returns an error if any dependency initialization fails
 */
func NewContainer(logger *log.Logger, dbPath string) (*Container, error) {
	container := &Container{
		logger: logger,
	}

	// Initialize GORM database connection with automatic schema migration
	db, err := initDatabase(dbPath)
	if err != nil {
		return nil, err
	}

	// Create repositories for each module
	container.cpuRepository = cpurepos.NewCPURepository(db)
	container.memoryRepository = memoryrepos.NewMemoryRepository(db)
	container.diskRepository = diskrepos.NewDiskRepository(db)
	container.networkRepository = networkrepos.NewNetworkRepository(db)
	container.dockerRepository = dockerrepos.NewDockerRepository(db)

	// Create individual services for each metric type
	container.cpuService = cpuservice.NewService(container.logger, container.cpuRepository)
	container.memoryService = memoryservice.NewService(container.logger, container.memoryRepository)
	container.diskService = diskservice.NewService(container.logger, container.diskRepository)
	container.networkService = networkservice.NewService(container.logger, container.networkRepository)
	container.dockerService = dockerservice.NewService(container.logger, dockercollectors.NewDockerMetricsCollector(container.logger), dockerrepos.NewDockerRepository(db))

	// Create system service that aggregates all metrics
	container.systemService = systemsrv.NewService(
		container.logger,
		container.cpuService,
		container.memoryService,
		container.diskService,
		container.networkService,
		container.dockerService,
	)

	// Create historical metrics service
	metricsCollector := historyapp.NewMetricsCollector(
		container.cpuService,
		container.memoryService,
		container.diskService,
		container.networkService,
		container.dockerService,
	)
	container.historicalMetricsService = historyapp.NewHistoricalMetricsService(
		container.logger,
		metricsCollector,
	)

	return container, nil
}

/**
 * initDatabase initializes the GORM database connection.
 * This function sets up the SQLite database connection for the application and performs
 * automatic schema migration for all historical metric entities.
 *
 * @param dbPath The file path to the SQLite database file
 * @return *gorm.DB The initialized GORM database instance
 * @return error Returns an error if database connection or migration fails
 */
func initDatabase(dbPath string) (*gorm.DB, error) {
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if err != nil {
		return nil, err
	}

	// Auto-migrate all historical metric entities to create database tables
	err = db.AutoMigrate(
		&cpuentities.HistoricalCPUMetric{},
		&memoryentities.HistoricalMemoryMetric{},
		&diskentities.HistoricalDiskMetric{},
		&networkentities.HistoricalNetworkMetric{},
		&dockerdomain.HistoricalDockerMetric{},
	)
	if err != nil {
		return nil, err
	}

	return db, nil
}

// Dependency getters - provide access to initialized components

/**
 * GetLogger returns the logger instance.
 * @return *log.Logger The logger instance
 */
func (c *Container) GetLogger() *log.Logger {
	return c.logger
}

/**
 * GetCPUService returns the CPU metrics service instance.
 * @return cpuservice.Service The CPU service instance
 */
func (c *Container) GetCPUService() cpuservice.Service {
	return c.cpuService
}

/**
 * GetMemoryService returns the memory metrics service instance.
 * @return memoryservice.Service The memory service instance
 */
func (c *Container) GetMemoryService() memoryservice.Service {
	return c.memoryService
}

/**
 * GetDiskService returns the disk metrics service instance.
 * @return diskservice.Service The disk service instance
 */
func (c *Container) GetDiskService() diskservice.Service {
	return c.diskService
}

/**
 * GetNetworkService returns the network metrics service instance.
 * @return networkservice.Service The network service instance
 */
func (c *Container) GetNetworkService() networkservice.Service {
	return c.networkService
}

/**
 * GetDockerService returns the docker metrics service instance.
 * @return dockerservice.Service The docker service instance
 */
func (c *Container) GetDockerService() dockerservice.Service {
	return c.dockerService
}

/**
 * GetSystemService returns the system metrics service instance.
 * @return systemsrv.Service The system service instance
 */
func (c *Container) GetSystemService() systemsrv.Service {
	return c.systemService
}

/**
 * GetHistoricalMetricsService returns the historical metrics service instance.
 * @return historycore.HistoricalMetricsService The historical metrics service instance
 */
func (c *Container) GetHistoricalMetricsService() historycore.HistoricalMetricsService {
	return c.historicalMetricsService
}
