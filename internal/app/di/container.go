package di

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
	raftcluster "system-stats/internal/cluster/raft"
	raftbridge "system-stats/internal/cluster/raft/bridge"
	metricstream "system-stats/internal/cluster/raft/metricstream"
	cpu "system-stats/internal/metrics/cpu"
	disk "system-stats/internal/metrics/disk"
	docker "system-stats/internal/metrics/docker"
	memory "system-stats/internal/metrics/memory"
	network "system-stats/internal/metrics/network"
	sensors "system-stats/internal/metrics/sensors"
	connectors "system-stats/internal/platform/connectors"
	health "system-stats/internal/platform/health"
	history "system-stats/internal/platform/history"
	setupcfg "system-stats/internal/platform/setup"
	system "system-stats/internal/platform/system"

	"github.com/charmbracelet/log"
)

// hostStaticsAdapter implements hosts.StaticHardwareSource over the metric
// repositories, reading each host's static hardware identity from its latest
// cpu/memory/disk rows for the /hosts response.
type hostStaticsAdapter struct {
	cpu  cpu.Repository
	mem  memory.Repository
	disk disk.Repository
}

func (a hostStaticsAdapter) HostStatics(ctx context.Context, hostID uint) hosts.HostStatics {
	var s hosts.HostStatics
	if m, err := a.cpu.GetLatestMetricByHost(ctx, hostID); err == nil && m != nil {
		s.CPUModel = m.ModelName
		s.CPUCores = m.Cores
	}
	if m, err := a.mem.GetLatestMetricByHost(ctx, hostID); err == nil && m != nil {
		s.MemoryTotal = m.Total
	}
	if m, err := a.disk.GetLatestMetricByHost(ctx, hostID); err == nil && m != nil {
		s.DiskTotal = m.Total
	}
	return s
}

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

	connectorRepository connectors.Repository

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

	// Best-effort metric stream (non-durable, off-Raft). The sink writes a
	// received batch into local state + SSE; the sender broadcasts this node's
	// metrics to peers; the receiver authenticates + ingests inbound batches.
	metricSink     *raftcluster.MetricSink
	metricSender   *metricstream.Sender
	metricReceiver *metricstream.Receiver
	// pbsSnapshotSink stores PBS detail snapshots carried by received metric
	// batches; registered by server wiring and (re)applied to the metric sink on
	// every Raft activation so it survives a wizard-driven join.
	pbsSnapshotSink func(context.Context, uint, json.RawMessage)
	// jwtSecret is the cluster-shared secret reused as the HMAC key for the
	// intra-cluster metric stream (all nodes of a cluster share it).
	jwtSecret string
	// envJWTSecret / envRefreshSecret are the BOOT env auth secrets, kept so the
	// secret reconciler can seed an empty cluster_config from the leader's env
	// (jwtSecret may later be swapped to the adopted cluster-shared value).
	envJWTSecret     string
	envRefreshSecret string

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
	// selfAdvertiseStarted guards the single self-advertise loop
	// (startSelfAdvertiseLoopLocked) so it is launched at most once.
	selfAdvertiseStarted bool
	// membershipStarted guards the single membership manager loop
	// (startMembershipManagerLocked) so it is launched at most once. It reads
	// the swappable Service live, so one long-lived loop survives re-activation.
	membershipStarted bool
	// secretReconcilerStarted guards the single cluster-secret reconciler loop
	// (startClusterSecretReconcilerLocked) so it is launched at most once.
	secretReconcilerStarted bool
	// postActivate is fired in a goroutine after every successful
	// Raft activation. Used by server.Run to (re-)start metrics
	// collection when the wizard's join branch flips Raft on
	// without going through /setup/complete.
	postActivate func()
}

// envDuration reads a Go duration from env with a default.
func envDuration(key string, def time.Duration) time.Duration {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return def
}

// SetPostActivateHook registers a callback fired after every successful
// ActivateRaft call. Safe to call multiple times; the latest hook wins.
func (c *Container) SetPostActivateHook(fn func()) {
	c.activateMu.Lock()
	defer c.activateMu.Unlock()
	c.postActivate = fn
}

// NewContainer creates a new dependency injection container.
//
// raftCfg is optional; when raftCfg.Enabled is false the legacy direct-write
// path is kept and raftService is a no-op DisabledService. The bridge
// pieces (picker, sender, receiver) are only constructed when both
// raftCfg.Enabled and raftCfg.Bridge.Enabled are true and a shared secret
// is present.
func NewContainer(logger *log.Logger, dbConfig config.DatabaseConfig, jwtSecret, refreshSecret string, startTime time.Time, raftCfg config.RaftConfig, traefikDirs, nginxDirs []string) (*Container, error) {
	container := &Container{
		logger:           logger,
		broker:           stream.NewBroker(),
		jwtSecret:        jwtSecret,
		envJWTSecret:     jwtSecret,
		envRefreshSecret: refreshSecret,
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
	container.connectorRepository = connectors.NewRepository(db)

	container.userRepository = users.NewUserRepository(db)
	container.refreshTokenRepository = users.NewRefreshTokenRepository(db)

	container.cpuService = cpu.NewService(container.logger, container.cpuRepository)
	container.memoryService = memory.NewService(container.logger, container.memoryRepository)
	container.diskService = disk.NewService(container.logger, container.diskRepository)
	container.networkService = network.NewService(container.logger, container.networkRepository)
	container.dockerService = docker.NewService(container.logger, docker.NewDockerCollector(container.logger, traefikDirs, nginxDirs), container.dockerRepository)
	container.hostService = hosts.NewService(container.logger, container.hostRepository)
	// Enrich /hosts with each host's static hardware identity (cpu model/cores,
	// ram/disk totals) read from its latest metric rows, so the node card gets
	// its static facts in that one request instead of the per-metric queries.
	hosts.AttachStaticHardwareSource(container.hostService, hostStaticsAdapter{
		cpu:  container.cpuRepository,
		mem:  container.memoryRepository,
		disk: container.diskRepository,
	})
	container.healthService = health.NewService(container.logger, container.hostRepository, startTime)
	container.sensorsService = sensors.NewService(container.logger)

	container.tokenService = users.NewTokenService(
		container.refreshTokenRepository,
		jwtSecret,
		refreshSecret,
		// Self-hosted dashboard: favour long sessions. Overridable via
		// AUTH_ACCESS_TTL / AUTH_REFRESH_TTL (Go durations, e.g. "30m", "720h").
		envDuration("AUTH_ACCESS_TTL", time.Hour),
		envDuration("AUTH_REFRESH_TTL", 90*24*time.Hour),
	)
	container.invRepository = invitations.NewRepository(db)
	container.invService = invitations.NewService(logger, container.invRepository)
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
	appliersDeps := raftcluster.AppliersDeps{
		Logger:           c.logger,
		DB:               c.db,
		ClusterID:        cfg.ClusterID,
		HostRepo:         c.hostRepository,
		UserRepo:         c.userRepository,
		RefreshTokenRepo: c.refreshTokenRepository,
		CPURepo:          c.cpuRepository,
		MemoryRepo:       c.memoryRepository,
		DiskRepo:         c.diskRepository,
		NetworkRepo:      c.networkRepository,
		DockerRepo:       c.dockerRepository,
		ConnectorRepo:    c.connectorRepository,
		Publish:          c.broker.Publish,
	}
	act, err := raftcluster.Activate(ctx, raftcluster.ActivationDeps{
		Logger:   c.logger,
		DB:       c.db,
		Appliers: appliersDeps,
	}, cfg, c.raftSwap)
	if err != nil {
		return nil, cfg, err
	}
	c.raftFSM = act.FSM
	c.raftCfgSnapshot = cfg

	// Best-effort metric stream (off-Raft). Built whenever Raft is active: the
	// intra-cluster P2P path needs only the cluster-shared JWT secret; the
	// cross-cluster uplink additionally uses the bridge secret (carried in cfg).
	c.metricSink = raftcluster.NewMetricSink(appliersDeps)
	if c.pbsSnapshotSink != nil {
		c.metricSink.SetPBSSink(c.pbsSnapshotSink)
	}
	c.metricSender = metricstream.NewSender(c.logger, c.db, cfg.ClusterID, cfg.NodeID, c.jwtSecret, cfg.Bridge)
	c.metricReceiver = metricstream.NewReceiver(c.logger, c.metricSink, cfg.ClusterID, c.jwtSecret, cfg.Bridge)

	// Tell the host repository which cluster id is OURS so its connector
	// cross-site guard only deflects rows from a genuinely DIFFERENT cluster.
	// Without this, this node's own Proxmox connector polling THIS machine
	// (the local agent is also a guest of its own local PVE) would be treated
	// as a remote collision, shedding id=1's topology and spawning a duplicate
	// row whose MAC then collides on the unique index every cycle.
	c.hostRepository.SetLocalClusterID(cfg.ClusterID)

	// Backfill any users present in the local SQLite but not yet in the
	// replicated Raft log. Critical when this node was the wizard's
	// "Start new cluster" leader: the user was just created via
	// user.Register, and we want every joiner to see them via snapshot.
	// Best-effort: a failure (e.g. follower, no leader yet) only means
	// the backfill will run again on the next ActivateRaft.
	if c.raftReplicator != nil && act.Node.IsLeader() {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if n, err := c.raftReplicator.BackfillLocalUsers(ctx, c.userRepository); err != nil {
				c.logger.Warn("raft: user backfill failed",
					"submitted", n, "error", err,
				)
			} else if n > 0 {
				c.logger.Info("raft: backfilled local users into replicated log",
					"submitted", n,
				)
			}
			// Same idea for hosts — the local-collector row inserted
			// at boot (id=1) and any other rows previously known
			// locally get republished so peers' dashboards show them.
			if n, err := c.raftReplicator.BackfillLocalHosts(ctx, c.hostRepository); err != nil {
				c.logger.Warn("raft: host backfill failed",
					"submitted", n, "error", err,
				)
			} else if n > 0 {
				c.logger.Info("raft: backfilled local hosts into replicated log",
					"submitted", n,
				)
			}
		}()
	}

	// Fire the post-activation hook (e.g. start metrics collection so
	// RegisterOrUpdateCurrentHost runs periodically and the local
	// collector keeps replicating its presence into the Raft log).
	// Set via SetPostActivateHook; nil on first activation is fine.
	if hook := c.postActivate; hook != nil {
		go hook()
	}

	// Build bridge primitives only when a shared secret is present —
	// without it the receiver would reject every request anyway.
	if cfg.Bridge.Enabled || cfg.Bridge.SharedSecret != "" {
		c.buildBridgeLocked(cfg.Bridge, cfg.ClusterID, cfg.NodeID, act.FSM)
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
	c.startSelfAdvertiseLoopLocked(ctx)
	c.startMembershipManagerLocked(ctx)
	c.startClusterSecretReconcilerLocked(ctx)
}

// startClusterSecretReconcilerLocked launches the single loop that keeps the
// cluster-shared auth secret (JWT/refresh) converged across all nodes. The
// metric-stream HMAC and cross-node sessions both depend on every node using
// the SAME key, but the boot/join paths can leave a node on its own env
// placeholder (cluster_config not seeded yet, or not adopted after a restart) —
// which surfaces as "bridge: signature mismatch" on every metric post and
// per-node-only logins. This loop self-heals it: the leader seeds an empty
// cluster_config from its env secret, and every node adopts the cluster_config
// secret (into the metric stream + token service) whenever it differs from what
// it's currently using. activateMu must be held.
func (c *Container) startClusterSecretReconcilerLocked(ctx context.Context) {
	if ctx == nil || c.secretReconcilerStarted || c.raftSwap == nil {
		return
	}
	c.secretReconcilerStarted = true
	go func() {
		first := time.NewTimer(5 * time.Second)
		defer first.Stop()
		t := time.NewTicker(20 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-first.C:
			case <-t.C:
			}
			c.reconcileClusterSecret(ctx)
		}
	}()
}

// reconcileClusterSecret performs one convergence pass (see the loop above).
func (c *Container) reconcileClusterSecret(ctx context.Context) {
	svc := c.GetRaftService()
	if svc == nil || !svc.Enabled() || c.db == nil {
		return
	}
	rc, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	gotJWT, _ := raftcluster.LookupClusterConfig(rc, c.db, "jwt_secret")
	gotRefresh, _ := raftcluster.LookupClusterConfig(rc, c.db, "refresh_secret")

	if gotJWT != "" && gotRefresh != "" {
		// Adopt the cluster-shared secret if we're not already on it. This is the
		// source of truth; the leader's own seeded value adopts as a no-op.
		c.activateMu.Lock()
		cur := c.jwtSecret
		c.activateMu.Unlock()
		if gotJWT != cur {
			c.SetClusterHMACSecret(gotJWT) // metric-stream HMAC
			if c.tokenService != nil {
				c.tokenService.SetSecrets(gotJWT, gotRefresh) // cross-node sessions
			}
			c.logger.Info("raft: adopted cluster-shared auth secret from cluster_config (metrics + sessions now aligned)")
		}
		return
	}

	// Not seeded yet: the leader publishes its env secret so the cluster
	// converges. Only the leader can commit; followers wait for replication.
	if svc.IsLeader() && c.envJWTSecret != "" && c.envRefreshSecret != "" {
		repl := c.GetRaftReplicator()
		if repl == nil {
			return
		}
		if err := repl.SubmitAuthSecretSet(rc, c.envJWTSecret, c.envRefreshSecret); err != nil {
			c.logger.Warn("raft: seed cluster-shared auth secret failed", "error", err)
		} else {
			c.logger.Info("raft: seeded cluster-shared auth secret from leader env")
		}
	}
}

// startMembershipManagerLocked launches the single Raft membership manager.
// It promotes healthy non-voters to voters and demotes long-unreachable
// voters, acting only while THIS node is the leader. It reads the swappable
// Service live, so one loop keeps working across a wipe-state re-activation.
// activateMu must be held.
func (c *Container) startMembershipManagerLocked(ctx context.Context) {
	if ctx == nil || c.membershipStarted || c.raftSwap == nil {
		return
	}
	c.membershipStarted = true
	mgr := raftcluster.NewMembershipManager(c.raftSwap, c.logger)
	go mgr.Run(ctx)
}

// startSelfAdvertiseLoopLocked launches a single background loop that
// (re)publishes THIS node's advertise URL into the replicated peer catalog.
// EVERY node advertises itself — not just the leader — because the best-effort
// intra-cluster metric fanout discovers its peers from this catalog (each node
// POSTs its metrics directly to every other node's HTTP URL). The leader applies
// its own advertise directly; a follower forwards it to the leader (which is why
// the leader still advertises promptly on gaining leadership, so followers have
// a leader URL to forward to). The advertise is an idempotent upsert and a no-op
// when Raft is disabled or no advertise URL is configured.
func (c *Container) startSelfAdvertiseLoopLocked(ctx context.Context) {
	if ctx == nil || c.selfAdvertiseStarted {
		return
	}
	c.selfAdvertiseStarted = true
	go func() {
		t := time.NewTicker(3 * time.Second)
		defer t.Stop()
		wasLeader := false
		ticks := 0
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
			svc := c.GetRaftService()
			enabled := svc != nil && svc.Enabled()
			isLeader := enabled && svc.IsLeader()
			// Advertise self when: we just gained leadership (so the leader URL
			// lands ASAP and followers can forward), OR every ~30s for every node
			// (covers a dropped catalog write, a restarted node, and keeps all
			// peers discoverable for the metric fanout).
			if enabled && ((!wasLeader && isLeader) || ticks%10 == 0) {
				c.AdvertiseSelfNow(ctx)
			}
			wasLeader = isLeader
			ticks++
		}
	}()
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
			Mode:         config.NormalizeBridgeMode(rt.BridgeMode),
		},
	}
	if cfg.DataDir == "" {
		cfg.DataDir = config.DefaultRaftDataDir()
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

	c.buildBridgeLocked(bridge, c.raftCfgSnapshot.ClusterID, c.raftCfgSnapshot.NodeID, c.raftFSM)
	c.startBridgeGoroutinesLocked()
	return nil
}

// buildBridgeLocked constructs picker/sender/receiver per the bridge mode:
// "push" (spoke uplink) runs the sender only, restricted to host/metric
// commands; "receive" (hub) runs the receiver only, with the same allowlist
// as defense in depth; "both" keeps the legacy symmetric pair. Seeds prime
// the picker — the replicated catalog can't know a peer cluster's URL before
// the bridge is up. activateMu must be held.
func (c *Container) buildBridgeLocked(bridge config.RaftBridgeConfig, clusterID, nodeID string, fsm *raftcluster.FSM) {
	mode := config.NormalizeBridgeMode(bridge.Mode)
	c.bridgePicker = nil
	c.bridgeSender = nil
	c.bridgeReceiver = nil

	if mode != config.BridgeModePush {
		c.bridgeReceiver = raftbridge.NewReceiver(c.logger, c.raftSwap, c.db, bridge.SharedSecret, clusterID).
			WithUplinkOnly(mode == config.BridgeModeReceive)
	}
	if mode != config.BridgeModeReceive && fsm != nil {
		c.bridgePicker = raftbridge.NewPicker(c.logger, c.db, clusterID, bridge.RemoteSeeds, c.raftCfgSnapshot.AdvertiseURL)
		c.bridgeSender = raftbridge.NewSender(c.logger, c.raftSwap, fsm.ApplyEvents(), c.bridgePicker, bridge.SharedSecret, clusterID, nodeID).
			WithUplinkOnly(mode == config.BridgeModePush)
		// Reconcile re-publishes ALL local host rows as own-origin entries —
		// correct for a push spoke (every row is its own), wrong for a legacy
		// symmetric pair (peer-origin rows would be relabelled): push-only.
		if mode == config.BridgeModePush {
			c.bridgeSender.WithReconcile(func(ctx context.Context) {
				bctx, cancel := context.WithTimeout(ctx, 30*time.Second)
				defer cancel()
				if n, err := c.raftReplicator.BackfillLocalHosts(bctx, c.hostRepository); err != nil {
					c.logger.Warn("bridge: reconcile backfill failed", "submitted", n, "error", err)
				} else if n > 0 {
					c.logger.Debug("bridge: reconcile backfill submitted", "hosts", n)
				}
			})
		}
	}

	// The off-Raft metric stream is built once at activation, but its cross-cluster
	// target is (re)configured here too so a live "Apply uplink" (ConfigureBridge)
	// re-points metrics at the same hub as the topology bridge instead of leaving
	// them shipping to the seeds captured at activation. Without this, a freshly
	// (re)created cluster that attaches its uplink at runtime replicates host
	// topology to the hub but never its metrics (the hub shows "--").
	if c.metricSender != nil {
		c.metricSender.SetBridge(bridge)
	}
	if c.metricReceiver != nil {
		c.metricReceiver.SetBridgeSecret(bridge.SharedSecret)
	}
}

// CurrentRaftConfig returns the last RaftConfig applied via ActivateRaft.
// Returns the zero value when Raft has never been activated.
func (c *Container) CurrentRaftConfig() config.RaftConfig {
	c.activateMu.Lock()
	defer c.activateMu.Unlock()
	return c.raftCfgSnapshot
}

// SeedClusterSecrets publishes the given auth secrets into the replicated
// cluster_config (CmdAuthSecretSet) so nodes that later join this cluster
// receive valid JWT signing keys via snapshot replay. Leader-only: a no-op
// when Raft is disabled or this node is not the leader. Used by the runtime
// "make this the main node" admin flow (the wizard relies on boot-time
// BootstrapClusterSecrets instead).
func (c *Container) SeedClusterSecrets(ctx context.Context, jwtSecret, refreshSecret string) error {
	if jwtSecret == "" || refreshSecret == "" {
		return nil
	}
	repl := c.GetRaftReplicator()
	svc := c.GetRaftService()
	if repl == nil || svc == nil || !svc.IsLeader() {
		return nil
	}
	return repl.SubmitAuthSecretSet(ctx, jwtSecret, refreshSecret)
}

// AdvertiseSelfNow publishes this node's advertised HTTP URL into the
// peer-URL catalog (CmdPeerNodeAdvertise) using the currently-active Raft
// config, so followers can forward writes to it. Best-effort and leader-only;
// a no-op when no advertise URL is configured (boot re-advertises anyway).
func (c *Container) AdvertiseSelfNow(ctx context.Context) {
	cfg := c.CurrentRaftConfig()
	if cfg.AdvertiseURL == "" {
		return
	}
	raftcluster.AdvertiseSelf(ctx, c.logger, c.GetRaftService(), c.GetRaftReplicator(), cfg.ClusterID, cfg.NodeID, cfg.AdvertiseURL)
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
	cv.RaftBridgeMode = ""
	if werr := cw.WriteConfigFile(cv); werr != nil {
		return werr
	}
	c.activateMu.Lock()
	c.raftBootError = ""
	c.activateMu.Unlock()
	return nil
}

// shutdownAndWipeLocked shuts the running raft node down and deletes the
// on-disk BoltDB log + snapshot files, leaving the SwappableService
// wrapping DisabledService. activateMu must be held.
func (c *Container) shutdownAndWipeLocked() error {
	prevCfg := c.raftCfgSnapshot
	if !c.raftSwap.Enabled() && prevCfg.NodeID == "" {
		return nil
	}
	if err := c.raftSwap.Close(); err != nil && c.logger != nil {
		c.logger.Warn("raft wipe: shutdown returned error (continuing)", "error", err)
	}
	c.raftSwap.Swap(raftcluster.NewDisabledService())
	c.raftFSM = nil
	c.bridgeSender = nil
	c.bridgePicker = nil
	c.bridgeReceiver = nil
	c.metricSink = nil
	c.metricSender = nil
	c.metricReceiver = nil

	dir := prevCfg.DataDir
	if dir == "" {
		dir = config.DefaultRaftDataDir()
	}
	if err := wipeDirContents(dir); err != nil {
		return fmt.Errorf("raft wipe: clear data dir: %w", err)
	}
	c.raftBootError = ""
	return nil
}

// RaftEnabled satisfies setup.RaftActivator. Reports whether the running
// raft layer is currently active.
func (c *Container) RaftEnabled() bool {
	if c.raftSwap == nil {
		return false
	}
	return c.raftSwap.Enabled()
}

// WipeRaftState shuts down the currently-running Raft node (if any),
// deletes the on-disk BoltDB log + snapshot files and re-activates the
// layer using the same RuntimeConfig but with Bootstrap=true. The
// replicated tables in SQLite (users, hosts, metrics, …) are left
// untouched — only the consensus log / cluster membership are reset.
//
// Used to recover from a wedged 2-voter cluster where a voter was
// added with an unreachable advertise address. After WipeRaftState
// completes the node comes back as a healthy single-voter cluster and
// can issue join tokens for additional voters.
func (c *Container) WipeRaftState() error {
	c.activateMu.Lock()
	defer c.activateMu.Unlock()

	prevCfg := c.raftCfgSnapshot
	if !c.raftSwap.Enabled() && prevCfg.NodeID == "" {
		return fmt.Errorf("raft: nothing to wipe (layer never activated)")
	}

	if err := c.shutdownAndWipeLocked(); err != nil {
		return err
	}

	// Re-activate as fresh bootstrap single-voter.
	prevCfg.Bootstrap = true
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_, _, err := c.activateLocked(ctx, prevCfg)
	if err != nil {
		c.raftBootError = err.Error()
		return err
	}
	return nil
}

// ShutdownAndWipeRaft tears down the running Raft node and clears its
// on-disk state without re-activating. Used by the setup wizard's join
// flow when the node is half-configured from a previous failed attempt:
// the caller follows up with ActivateRaft using fresh parameters from
// the wizard form. SQLite tables (users, hosts, metrics) are untouched.
func (c *Container) ShutdownAndWipeRaft() error {
	c.activateMu.Lock()
	defer c.activateMu.Unlock()
	return c.shutdownAndWipeLocked()
}

// wipeDirContents removes every entry inside dir but keeps dir itself.
func wipeDirContents(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, e := range entries {
		if err := os.RemoveAll(filepath.Join(dir, e.Name())); err != nil {
			return err
		}
	}
	return nil
}

// FactoryResetRaft fully decouples this node from any Raft cluster: shuts
// down the live node, wipes the on-disk log + snapshot, and removes every
// RAFT_* entry from .env so the next process boot comes up Raft-disabled.
// SQLite data (users, hosts, metrics) is preserved.
//
// Use this when a multi-node cluster is wedged (e.g. one voter is
// unreachable forever — typically a Docker port-mapping mistake — and
// the remaining voters can't reach quorum). Apply on EVERY node, then
// re-run the setup wizard from scratch.
func (c *Container) FactoryResetRaft() error {
	c.activateMu.Lock()
	defer c.activateMu.Unlock()
	if err := c.shutdownAndWipeLocked(); err != nil {
		return err
	}
	cw := setupcfg.NewConfigWriter()
	cv, _ := cw.ReadCurrentConfig()
	if cv == nil {
		return nil
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
	cv.RaftBridgeMode = ""
	c.raftBootError = ""
	c.raftCfgSnapshot = config.RaftConfig{}
	return cw.WriteConfigFile(cv)
}

// running bridge primitives, then persists the new values into .env so
// the change survives a restart. The advertiseURL argument is optional;
// pass "" to leave the previously configured URL untouched.
func (c *Container) SaveBridge(secret string, remoteSeeds []string, advertiseURL, mode string) error {
	bridge := config.RaftBridgeConfig{
		Enabled:      true,
		SharedSecret: secret,
		RemoteSeeds:  remoteSeeds,
		Mode:         config.NormalizeBridgeMode(mode),
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
	cv.RaftBridgeMode = bridge.Mode
	if advertiseURL != "" {
		cv.RaftAdvertisePublicURL = advertiseURL
	}
	return cw.WriteConfigFile(cv)
}

// BridgeSettings returns the currently configured uplink parameters so the
// admin form can prefill instead of starting blank — re-applying a blank
// form used to wipe the hub URL (remote seeds) and stall the uplink.
func (c *Container) BridgeSettings() (mode, secret string, seeds []string, advertiseURL string) {
	c.activateMu.Lock()
	b := c.raftCfgSnapshot.Bridge
	advertiseURL = c.raftCfgSnapshot.AdvertiseURL
	c.activateMu.Unlock()
	if b.SharedSecret == "" || len(b.RemoteSeeds) == 0 || b.Mode == "" || advertiseURL == "" {
		cw := setupcfg.NewConfigWriter()
		if cv, _ := cw.ReadCurrentConfig(); cv != nil {
			if b.SharedSecret == "" {
				b.SharedSecret = strings.TrimSpace(cv.RaftBridgeSharedSecret)
			}
			if len(b.RemoteSeeds) == 0 {
				for _, s := range strings.Split(cv.RaftBridgeRemoteSeeds, ",") {
					if v := strings.TrimSpace(s); v != "" {
						b.RemoteSeeds = append(b.RemoteSeeds, v)
					}
				}
			}
			if b.Mode == "" {
				b.Mode = cv.RaftBridgeMode
			}
			if advertiseURL == "" {
				advertiseURL = strings.TrimSpace(cv.RaftAdvertisePublicURL)
			}
		}
	}
	return config.NormalizeBridgeMode(b.Mode), b.SharedSecret, b.RemoteSeeds, advertiseURL
}

// BridgeSecret returns the currently configured uplink HMAC secret so an
// admin can copy it later when enrolling another site — it is otherwise
// write-only through the form and unrecoverable after a page reload.
// Empty when no bridge is configured.
func (c *Container) BridgeSecret() string {
	c.activateMu.Lock()
	if s := strings.TrimSpace(c.raftCfgSnapshot.Bridge.SharedSecret); s != "" {
		c.activateMu.Unlock()
		return s
	}
	c.activateMu.Unlock()
	cw := setupcfg.NewConfigWriter()
	if cv, _ := cw.ReadCurrentConfig(); cv != nil {
		return strings.TrimSpace(cv.RaftBridgeSharedSecret)
	}
	return ""
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

// GetHostRepository exposes the hosts repository for platform services that
// need read access outside the Service surface (e.g. connector MAC matching).
func (c *Container) GetHostRepository() hosts.Repository {
	return c.hostRepository
}

// GetConnectorRepository returns the connector registry repository.
func (c *Container) GetConnectorRepository() connectors.Repository {
	return c.connectorRepository
}

// GetCPURepository / GetMemoryRepository / GetDiskRepository /
// GetNetworkRepository expose metric repositories for the connector pollers'
// standalone (non-Raft) direct-write path.
func (c *Container) GetCPURepository() cpu.Repository         { return c.cpuRepository }
func (c *Container) GetMemoryRepository() memory.Repository   { return c.memoryRepository }
func (c *Container) GetDiskRepository() disk.Repository       { return c.diskRepository }
func (c *Container) GetNetworkRepository() network.Repository { return c.networkRepository }

// GetRefreshTokenRepository exposes the refresh-token repo for retention pruning.
func (c *Container) GetRefreshTokenRepository() users.RefreshTokenRepository {
	return c.refreshTokenRepository
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

// BridgeInfo returns the bridge runtime view for /raft/status: configured
// mode plus the sender's ship health (nil when the bridge isn't built).
func (c *Container) BridgeInfo() any {
	c.activateMu.Lock()
	defer c.activateMu.Unlock()
	if c.bridgeSender == nil && c.bridgeReceiver == nil {
		return nil
	}
	out := map[string]any{
		"mode": config.NormalizeBridgeMode(c.raftCfgSnapshot.Bridge.Mode),
	}
	if c.bridgeSender != nil {
		out["sender"] = c.bridgeSender.Snapshot()
	}
	out["receiving"] = c.bridgeReceiver != nil
	return out
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

// GetMetricSender returns the best-effort metric-stream sender, or nil when Raft
// is inactive. Called each collection tick to broadcast this node's metrics.
func (c *Container) GetMetricSender() *metricstream.Sender {
	c.activateMu.Lock()
	defer c.activateMu.Unlock()
	return c.metricSender
}

// SetClusterHMACSecret swaps the cluster-shared HMAC key (the JWT secret) used by
// the off-Raft metric stream. On a JOINED node the boot env JWT secret is a
// placeholder until BootstrapClusterSecrets (or the join secret-swap) discovers
// the real cluster-shared key from cluster_config; the metric sender/receiver
// captured the placeholder at activation, so without this they'd sign/verify
// same-cluster posts with the wrong key and every peer silently rejects them
// (cross-node metrics never sync). Idempotent and a no-op for an empty key or
// when nothing changed. Also updates any sender/receiver rebuilt on a later
// (re)activation by remembering the new value on c.jwtSecret.
func (c *Container) SetClusterHMACSecret(jwtSecret string) {
	if jwtSecret == "" {
		return
	}
	c.activateMu.Lock()
	defer c.activateMu.Unlock()
	if jwtSecret == c.jwtSecret {
		return
	}
	c.jwtSecret = jwtSecret
	if c.metricSender != nil {
		c.metricSender.SetIntraSecret(jwtSecret)
	}
	if c.metricReceiver != nil {
		c.metricReceiver.SetIntraSecret(jwtSecret)
	}
}

// SetPBSSnapshotSink registers the handler that stores a PBS detail snapshot
// carried by a received metric batch (the pbs poller's IngestRemoteSnapshot).
// It applies to the current metric sink and to any sink rebuilt on a later Raft
// activation, so a wizard-driven join still wires it without a restart.
func (c *Container) SetPBSSnapshotSink(fn func(context.Context, uint, json.RawMessage)) {
	c.activateMu.Lock()
	defer c.activateMu.Unlock()
	c.pbsSnapshotSink = fn
	if c.metricSink != nil {
		c.metricSink.SetPBSSink(fn)
	}
}

// GetMetricReceiver returns the best-effort metric-stream receiver handler, or
// nil when Raft is inactive. Mounted at metricstream.Path.
func (c *Container) GetMetricReceiver() *metricstream.Receiver {
	c.activateMu.Lock()
	defer c.activateMu.Unlock()
	return c.metricReceiver
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
