package di

import (
	"context"
	"fmt"
	"strings"
	"sync"
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
	raftbridge "system-stats/internal/cluster/raft/bridge"
	cpu "system-stats/internal/metrics/cpu"
	disk "system-stats/internal/metrics/disk"
	docker "system-stats/internal/metrics/docker"
	memory "system-stats/internal/metrics/memory"
	network "system-stats/internal/metrics/network"
	sensors "system-stats/internal/metrics/sensors"
	health "system-stats/internal/platform/health"
	history "system-stats/internal/platform/history"
	setupcfg "system-stats/internal/platform/setup"
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

	// raftSwap is the stable Service handle: every consumer (handlers,
	// replicator, services) keeps a pointer to it. Its inner Service is
	// flipped from DisabledService to a real Node by ActivateRaft.
	raftSwap       *raftcluster.SwappableService
	raftFSM        *raftcluster.FSM
	raftReplicator *raftcluster.Replicator
	bridgePicker   *raftbridge.Picker
	bridgeSender   *raftbridge.Sender
	bridgeReceiver *raftbridge.Receiver

	// activateMu serialises ActivateRaft / ConfigureBridge / Close.
	activateMu sync.Mutex
	// appCtx is the long-running context used by the bridge sender /
	// picker goroutines. Set once by SetAppContext before any activation
	// is triggered (server.go does that right after appCtx is created).
	appCtx context.Context
	// raftCfgSnapshot is the most recently activated RaftConfig — used by
	// ConfigureBridge to rebuild the sender / picker / receiver without
	// re-asking the caller for cluster id / node id.
	raftCfgSnapshot config.RaftConfig
	// raftBootError captures the message from a failed boot-time
	// activation so the admin UI can surface it ("Raft is disabled
	// because port :7000 is in use; reconfigure below").
	raftBootError string
}

// NewContainer creates a new dependency injection container.
//
// raftCfg is optional; when raftCfg.Enabled is false the legacy direct-write
// path is kept and raftService is a no-op DisabledService. The bridge
// pieces (picker, sender, receiver) are only constructed when both
// raftCfg.Enabled and raftCfg.Bridge.Enabled are true and a shared secret
// is present.
func NewContainer(logger *log.Logger, dbConfig config.DatabaseConfig, jwtSecret, refreshSecret string, startTime time.Time, raftCfg config.RaftConfig) (*Container, error) {
	container := &Container{
		logger: logger,
		broker: stream.NewBroker(),
	}

	db, err := database.Initialize(dbConfig)
	if err != nil {
		return nil, err
	}

	if err := database.Migrate(db); err != nil {
		return nil, err
	}

	container.db = db

	// Always create the SwappableService so every consumer can hold a
	// stable Service reference. Initially it wraps DisabledService; when
	// the env already requested RAFT_ENABLED=true (or the setup wizard
	// later calls ActivateRaft) we swap in a real Node.
	container.raftSwap = raftcluster.NewSwappableService(raftcluster.NewDisabledService())

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

	// Replicator is always wired to the SwappableService so writes
	// publishedduring the legacy code path (Raft off) are no-ops; once
	// ActivateRaft swaps a real Node in, the same Replicator starts
	// publishing CmdHostUpsert / CmdUserUpsert without further plumbing.
	container.raftReplicator = raftcluster.NewReplicator(container.raftSwap)
	hosts.AttachRaftReplicator(container.hostService, container.raftReplicator)
	users.AttachRaftReplicator(container.userService, container.raftReplicator)

	// If RAFT_ENABLED is true at boot, activate eagerly using env-derived
	// config. This preserves the existing "configure-via-env" workflow
	// for ops who don't go through the wizard.
	//
	// Failure here is INTENTIONALLY NOT FATAL: a stale .env (e.g. port
	// already taken on the host, a leftover RAFT_BOOTSTRAP=true after a
	// previous failed wizard run) used to crash-loop the process and
	// soft-brick the deployment. We now log a clear warning, leave
	// raftSwap as DisabledService and let the operator recover via the
	// wizard / admin panel. The Raft tab in /admin/nodes shows the
	// failure reason; running setup wizard again hot-activates with
	// updated parameters.
	if raftCfg.Enabled {
		if _, _, err := container.activateLocked(context.Background(), raftCfg); err != nil {
			container.raftBootError = err.Error()
			logger.Warn("raft: boot-time activation failed; continuing with Raft disabled",
				"error", err,
				"node_id", raftCfg.NodeID,
				"bind_addr", raftCfg.BindAddr,
				"advertise", raftCfg.AdvertiseAddr,
				"hint", "check the address is free (e.g. lsof -i :7000) or edit .env / use the admin panel to reconfigure",
			)
		}
	}

	return container, nil
}

// activateLocked performs the real activation. activateMu must be held.
// Returns the freshly built FSM + chosen RaftConfig for callers that want
// to log / store it.
func (c *Container) activateLocked(ctx context.Context, cfg config.RaftConfig) (*raftcluster.FSM, config.RaftConfig, error) {
	if c.raftSwap == nil {
		return nil, cfg, fmt.Errorf("raft: swappable wrapper not initialised")
	}
	if c.raftSwap.Enabled() {
		return c.raftFSM, c.raftCfgSnapshot, nil
	}
	act, err := raftcluster.Activate(ctx, raftcluster.ActivationDeps{
		Logger: c.logger,
		DB:     c.db,
		Appliers: raftcluster.AppliersDeps{
			Logger:           c.logger,
			DB:               c.db,
			HostRepo:         c.hostRepository,
			UserRepo:         c.userRepository,
			RefreshTokenRepo: c.refreshTokenRepository,
		},
	}, cfg, c.raftSwap)
	if err != nil {
		return nil, cfg, err
	}
	c.raftFSM = act.FSM
	c.raftCfgSnapshot = cfg

	// Build bridge primitives only when a shared secret is present —
	// without it the receiver would reject every request anyway.
	if cfg.Bridge.Enabled || cfg.Bridge.SharedSecret != "" {
		c.bridgePicker = raftbridge.NewPicker(c.logger, c.db, cfg.ClusterID)
		c.bridgeReceiver = raftbridge.NewReceiver(c.logger, c.raftSwap, c.db, cfg.Bridge.SharedSecret, cfg.ClusterID)
		c.bridgeSender = raftbridge.NewSender(c.logger, c.raftSwap, act.FSM.ApplyEvents(), c.bridgePicker, cfg.Bridge.SharedSecret, cfg.ClusterID, cfg.NodeID)
	}
	// Start bridge goroutines if appCtx is already set (server startup
	// has run and we're being called from the wizard); otherwise the
	// startup code path will start them right after SetAppContext.
	if c.appCtx != nil {
		c.startBridgeGoroutinesLocked()
	}
	return act.FSM, cfg, nil
}

// startBridgeGoroutinesLocked spawns picker.Run / sender.Run if they were
// constructed and not yet running. Safe to call multiple times — the
// goroutines self-terminate when appCtx is cancelled.
func (c *Container) startBridgeGoroutinesLocked() {
	if c.appCtx == nil {
		return
	}
	if c.bridgePicker != nil {
		go c.bridgePicker.Run(c.appCtx)
	}
	if c.bridgeSender != nil {
		go c.bridgeSender.Run(c.appCtx)
	}
}

// SetAppContext records the long-running app context so post-startup
// activations (wizard hot-init) can spawn bridge goroutines against it.
// Called once from server.go right after appCtx is constructed.
func (c *Container) SetAppContext(ctx context.Context) {
	c.activateMu.Lock()
	defer c.activateMu.Unlock()
	c.appCtx = ctx
	c.startBridgeGoroutinesLocked()
}

// ActivateRaft hot-initialises the Raft layer from a runtime config
// (typically built from the setup wizard). It is idempotent — calling it
// a second time after a successful activation is a no-op. Once Raft is
// active the only mutable knobs are the bridge fields, applied via
// ConfigureBridge.
func (c *Container) ActivateRaft(ctx context.Context, rt raftcluster.RuntimeConfig) error {
	cfg := config.RaftConfig{
		Enabled:       true,
		ClusterID:     rt.ClusterID,
		NodeID:        rt.NodeID,
		BindAddr:      rt.BindAddr,
		AdvertiseAddr: rt.AdvertiseAddr,
		DataDir:       rt.DataDir,
		Bootstrap:     rt.Bootstrap,
		AdvertiseURL:  rt.AdvertiseURL,
		Bridge: config.RaftBridgeConfig{
			Enabled:      rt.BridgeEnabled,
			SharedSecret: rt.BridgeSharedSecret,
			RemoteSeeds:  rt.BridgeRemoteSeeds,
		},
	}
	if cfg.DataDir == "" {
		cfg.DataDir = "./data/raft"
	}
	if cfg.BindAddr == "" {
		cfg.BindAddr = ":7000"
	}
	c.activateMu.Lock()
	defer c.activateMu.Unlock()
	_, _, err := c.activateLocked(ctx, cfg)
	return err
}

// ConfigureBridge updates the cross-cluster bridge configuration at
// runtime — shared secret, remote seeds, advertise URL. Tearing down and
// rebuilding the sender/picker is cheap; the receiver simply picks up the
// new secret next request.
//
// Requires the Raft layer to already be active.
func (c *Container) ConfigureBridge(bridge config.RaftBridgeConfig, advertiseURL string) error {
	c.activateMu.Lock()
	defer c.activateMu.Unlock()
	if c.raftSwap == nil || !c.raftSwap.Enabled() {
		return fmt.Errorf("raft: activate the layer first")
	}
	c.raftCfgSnapshot.Bridge = bridge
	if advertiseURL != "" {
		c.raftCfgSnapshot.AdvertiseURL = advertiseURL
	}

	c.bridgePicker = raftbridge.NewPicker(c.logger, c.db, c.raftCfgSnapshot.ClusterID)
	c.bridgeReceiver = raftbridge.NewReceiver(c.logger, c.raftSwap, c.db, bridge.SharedSecret, c.raftCfgSnapshot.ClusterID)
	if c.raftFSM != nil {
		c.bridgeSender = raftbridge.NewSender(c.logger, c.raftSwap, c.raftFSM.ApplyEvents(), c.bridgePicker, bridge.SharedSecret, c.raftCfgSnapshot.ClusterID, c.raftCfgSnapshot.NodeID)
	}
	c.startBridgeGoroutinesLocked()
	return nil
}

// CurrentRaftConfig returns the last RaftConfig applied via ActivateRaft.
// Returns the zero value when Raft has never been activated.
func (c *Container) CurrentRaftConfig() config.RaftConfig {
	c.activateMu.Lock()
	defer c.activateMu.Unlock()
	return c.raftCfgSnapshot
}

// RaftBootError returns the most recent boot-time activation failure (e.g.
// "bind: address already in use"). Empty when Raft was either never
// enabled or successfully activated. The admin Raft tab surfaces this so
// the operator knows why /api/v1/raft/status reports Raft as disabled.
func (c *Container) RaftBootError() string {
	c.activateMu.Lock()
	defer c.activateMu.Unlock()
	return c.raftBootError
}

// ResetRaftConfig wipes RAFT_* env entries from the persisted .env so the
// next process restart boots in Raft-disabled mode. Useful when the
// operator wants to abandon a half-configured cluster without editing
// .env by hand. Does not touch the running process — the SwappableService
// remains whatever it currently wraps.
func (c *Container) ResetRaftConfig() error {
	cw := setupcfg.NewConfigWriter()
	cv, err := cw.ReadCurrentConfig()
	if err != nil || cv == nil {
		return err
	}
	cv.RaftEnabled = ""
	cv.RaftClusterID = ""
	cv.RaftNodeID = ""
	cv.RaftBindAddr = ""
	cv.RaftAdvertiseAddr = ""
	cv.RaftDataDir = ""
	cv.RaftBootstrap = ""
	cv.RaftAdvertisePublicURL = ""
	cv.RaftBridgeEnabled = ""
	cv.RaftBridgeSharedSecret = ""
	cv.RaftBridgeRemoteSeeds = ""
	if werr := cw.WriteConfigFile(cv); werr != nil {
		return werr
	}
	c.activateMu.Lock()
	c.raftBootError = ""
	c.activateMu.Unlock()
	return nil
}

// SaveBridge satisfies raftcluster.BridgeConfigurator. It updates the
// running bridge primitives, then persists the new values into .env so
// the change survives a restart. The advertiseURL argument is optional;
// pass "" to leave the previously configured URL untouched.
func (c *Container) SaveBridge(secret string, remoteSeeds []string, advertiseURL string) error {
	bridge := config.RaftBridgeConfig{
		Enabled:      true,
		SharedSecret: secret,
		RemoteSeeds:  remoteSeeds,
	}
	if err := c.ConfigureBridge(bridge, advertiseURL); err != nil {
		return err
	}
	// Persist into .env so the change is durable. Best-effort: a write
	// failure here doesn't roll back the live update — the next ops
	// touchpoint can re-do it.
	cw := setupcfg.NewConfigWriter()
	cv, _ := cw.ReadCurrentConfig()
	if cv == nil {
		return nil
	}
	cv.RaftBridgeEnabled = "true"
	cv.RaftBridgeSharedSecret = secret
	cv.RaftBridgeRemoteSeeds = strings.Join(remoteSeeds, ",")
	if advertiseURL != "" {
		cv.RaftAdvertisePublicURL = advertiseURL
	}
	return cw.WriteConfigFile(cv)
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

// GetRaftService returns the Raft consensus service. It's always a
// SwappableService — the inner implementation flips from DisabledService
// to a real Node on ActivateRaft.
func (c *Container) GetRaftService() raftcluster.Service {
	return c.raftSwap
}

// GetRaftFSM returns the FSM used to register CommandAppliers, Snapshotter
// and Restorer. Returns nil until ActivateRaft has been called.
func (c *Container) GetRaftFSM() *raftcluster.FSM {
	c.activateMu.Lock()
	defer c.activateMu.Unlock()
	return c.raftFSM
}

// GetRaftReplicator returns the helper used by services to publish writes
// into the Raft log. Returns nil when Raft is disabled.
func (c *Container) GetRaftReplicator() *raftcluster.Replicator {
	return c.raftReplicator
}

// GetBridgePicker returns the cross-cluster URL latency picker, or nil when
// the bridge is not configured. Safe to read concurrently with
// ConfigureBridge — the reference returned is whichever picker was
// installed at the time of the call.
func (c *Container) GetBridgePicker() *raftbridge.Picker {
	c.activateMu.Lock()
	defer c.activateMu.Unlock()
	return c.bridgePicker
}

// GetBridgeSender returns the cross-cluster sender, or nil.
func (c *Container) GetBridgeSender() *raftbridge.Sender {
	c.activateMu.Lock()
	defer c.activateMu.Unlock()
	return c.bridgeSender
}

// GetBridgeReceiver returns the cross-cluster receiver handler, or nil.
func (c *Container) GetBridgeReceiver() *raftbridge.Receiver {
	c.activateMu.Lock()
	defer c.activateMu.Unlock()
	return c.bridgeReceiver
}

// Close releases resources held by the container. Currently shuts down the
// Raft node cleanly so its BoltDB log/stable stores are flushed.
func (c *Container) Close() error {
	c.activateMu.Lock()
	defer c.activateMu.Unlock()
	if c.raftSwap != nil {
		return c.raftSwap.Close()
	}
	return nil
}
