package di

import (
	"context"
	"time"

	"gorm.io/gorm"

	"system-stats/internal/app/config"
	"system-stats/internal/app/database"
	"system-stats/internal/app/stream"

	invitations "system-stats/internal/auth/invitations"
	users "system-stats/internal/auth/users"
	hosts "system-stats/internal/cluster/hosts"
	nodes "system-stats/internal/cluster/nodes"
	raftcluster "system-stats/internal/cluster/raft"
	cpu "system-stats/internal/metrics/cpu"
	disk "system-stats/internal/metrics/disk"
	docker "system-stats/internal/metrics/docker"
	memory "system-stats/internal/metrics/memory"
	network "system-stats/internal/metrics/network"
	sensors "system-stats/internal/metrics/sensors"
	health "system-stats/internal/platform/health"
	history "system-stats/internal/platform/history"
	system "system-stats/internal/platform/system"

	"github.com/charmbracelet/log"
)

// Container holds all application dependencies.
type Container struct {
	logger *log.Logger
	db     *gorm.DB

	cpuRepository     cpu.Repository
	memoryRepository  memory.Repository
	diskRepository    disk.Repository
	networkRepository network.Repository
	dockerRepository  docker.DockerRepository
	hostRepository    hosts.Repository

	userRepository         users.UserRepository
	refreshTokenRepository users.RefreshTokenRepository

	cpuService     cpu.Service
	memoryService  memory.Service
	diskService    disk.Service
	networkService network.Service
	dockerService  docker.Service
	hostService    hosts.Service
	healthService  health.Service

	userService  users.UserService
	tokenService users.TokenService

	invRepository invitations.Repository
	invService    invitations.Service

	nodeJoinTokenRepo nodes.JoinTokenRepository
	nodeCredRepo      nodes.CredentialRepository
	nodeService       nodes.Service

	systemService            system.Service
	historicalMetricsService history.HistoricalMetricsService
	sensorsService           sensors.Service

	broker *stream.Broker

	raftService raftcluster.Service
	raftFSM     *raftcluster.FSM
}

// NewContainer creates a new dependency injection container.
//
// raftCfg is optional; when raftCfg.Enabled is false the legacy direct-write
// path is kept and raftService is a no-op DisabledService.
func NewContainer(logger *log.Logger, dbConfig config.DatabaseConfig, jwtSecret, refreshSecret string, startTime time.Time, raftCfg config.RaftConfig) (*Container, error) {
	container := &Container{
		logger: logger,
		broker: stream.NewBroker(),
	}

	// Build the Raft layer early so other services can be wired with the
	// FSM / Service if they need to translate writes into commands. When
	// disabled this returns a no-op service and a nil FSM.
	raftSvc, raftFSM, err := raftcluster.New(context.Background(), logger, raftCfg)
	if err != nil {
		return nil, err
	}
	container.raftService = raftSvc
	container.raftFSM = raftFSM

	db, err := database.Initialize(dbConfig)
	if err != nil {
		return nil, err
	}

	if err := database.Migrate(db); err != nil {
		return nil, err
	}

	container.db = db

	container.cpuRepository = cpu.NewRepository(db)
	container.memoryRepository = memory.NewRepository(db)
	container.diskRepository = disk.NewRepository(db)
	container.networkRepository = network.NewRepository(db)
	container.dockerRepository = docker.NewRepository(db)
	container.hostRepository = hosts.NewRepository(db)
	container.nodeJoinTokenRepo = nodes.NewJoinTokenRepository(db)
	container.nodeCredRepo = nodes.NewCredentialRepository(db)

	container.userRepository = users.NewUserRepository(db)
	container.refreshTokenRepository = users.NewRefreshTokenRepository(db)

	container.cpuService = cpu.NewService(container.logger, container.cpuRepository)
	container.memoryService = memory.NewService(container.logger, container.memoryRepository)
	container.diskService = disk.NewService(container.logger, container.diskRepository)
	container.networkService = network.NewService(container.logger, container.networkRepository)
	container.dockerService = docker.NewService(container.logger, docker.NewDockerCollector(container.logger), container.dockerRepository)
	container.hostService = hosts.NewService(container.logger, container.hostRepository, container.nodeCredRepo)
	container.healthService = health.NewService(container.logger, container.hostRepository, container.nodeCredRepo, startTime)
	container.sensorsService = sensors.NewService(container.logger)

	container.tokenService = users.NewTokenService(
		container.refreshTokenRepository,
		jwtSecret,
		refreshSecret,
		15*time.Minute,
		720*time.Hour,
	)
	container.invRepository = invitations.NewRepository(db)
	container.invService = invitations.NewService(logger, container.invRepository)
	container.nodeService = nodes.NewService(logger, container.nodeJoinTokenRepo, container.nodeCredRepo, container.hostRepository)
	container.userService = users.NewUserService(container.userRepository, container.tokenService, container.invService)

	container.systemService = system.NewService(
		container.logger,
		container.cpuService,
		container.memoryService,
		container.diskService,
		container.networkService,
		container.dockerService,
	)

	metricsCollector := history.NewMetricsCollector(
		container.cpuService,
		container.memoryService,
		container.diskService,
		container.networkService,
		container.dockerService,
	)
	container.historicalMetricsService = history.NewHistoricalMetricsService(
		container.logger,
		metricsCollector,
		container.hostService,
	)

	return container, nil
}

func (c *Container) GetLogger() *log.Logger {
	return c.logger
}

func (c *Container) GetCPUService() cpu.Service {
	return c.cpuService
}

func (c *Container) GetMemoryService() memory.Service {
	return c.memoryService
}

func (c *Container) GetDiskService() disk.Service {
	return c.diskService
}

func (c *Container) GetNetworkService() network.Service {
	return c.networkService
}

func (c *Container) GetDockerService() docker.Service {
	return c.dockerService
}

func (c *Container) GetHostService() hosts.Service {
	return c.hostService
}

func (c *Container) GetHealthService() health.Service {
	return c.healthService
}

func (c *Container) GetSystemService() system.Service {
	return c.systemService
}

func (c *Container) GetSensorsService() sensors.Service {
	return c.sensorsService
}

func (c *Container) GetUserService() users.UserService {
	return c.userService
}

func (c *Container) GetTokenService() users.TokenService {
	return c.tokenService
}

func (c *Container) GetInvitationService() invitations.Service {
	return c.invService
}

func (c *Container) GetNodeService() nodes.Service {
	return c.nodeService
}

func (c *Container) GetHistoricalMetricsService() history.HistoricalMetricsService {
	return c.historicalMetricsService
}

func (c *Container) GetDB() *gorm.DB {
	return c.db
}

func (c *Container) GetBroker() *stream.Broker {
	return c.broker
}

// GetRaftService returns the Raft consensus service. When RAFT_ENABLED=false
// this returns a no-op DisabledService whose SubmitCommand returns
// raftcluster.ErrDisabled, signalling callers to fall back to direct writes.
func (c *Container) GetRaftService() raftcluster.Service {
	return c.raftService
}

// GetRaftFSM returns the FSM used to register CommandAppliers, Snapshotter
// and Restorer. Returns nil when Raft is disabled.
func (c *Container) GetRaftFSM() *raftcluster.FSM {
	return c.raftFSM
}

// Close releases resources held by the container. Currently shuts down the
// Raft node cleanly so its BoltDB log/stable stores are flushed.
func (c *Container) Close() error {
	if c.raftService != nil {
		return c.raftService.Close()
	}
	return nil
}
