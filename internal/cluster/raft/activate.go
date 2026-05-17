package raft

import (
	"context"
	"fmt"

	"github.com/charmbracelet/log"
	"gorm.io/gorm"

	"system-stats/internal/app/config"
)

// ActivationDeps bundles everything the runtime activator needs to wire a
// fresh Raft node into an already-running process.
type ActivationDeps struct {
	Logger   *log.Logger
	DB       *gorm.DB
	Appliers AppliersDeps
}

// Activation is the result of a successful Activate call. The caller
// (DI container) owns these and is responsible for starting any
// dependent goroutines (bridge sender / picker / receiver — built
// separately because they need appCtx).
type Activation struct {
	Node *Node
	FSM  *FSM
}

// RuntimeConfig is the small projection of config.RaftConfig used by the
// setup wizard's "Start new cluster" / "Join existing cluster" branches.
// It mirrors the env-driven RaftConfig but the setup package depends only
// on this type (avoiding an import cycle through internal/app/config).
type RuntimeConfig struct {
	ClusterID     string
	NodeID        string
	BindAddr      string
	AdvertiseAddr string
	DataDir       string
	Bootstrap     bool
	AdvertiseURL  string

	BridgeEnabled      bool
	BridgeSharedSecret string
	BridgeRemoteSeeds  []string
}

// Activate builds and starts a real Raft Node with the given configuration,
// wires the SQLite snapshotter / restorer and registers every CommandApplier.
// On success it Swaps the swappable wrapper to point at the new Node so the
// Replicator (and every handler holding the SwappableService) immediately
// see the new implementation.
//
// Caller responsibilities:
//   - Build a Replicator wrapping `swap` once at startup and attach it to
//     hosts.Service / users.Service. After Activate returns those services
//     start publishing CmdHostUpsert / CmdUserUpsert without any further
//     wiring.
//   - Start the bridge picker / sender / receiver goroutines if the
//     configuration enables the bridge — Activate builds the Node but
//     leaves bridge orchestration to the DI container that owns appCtx.
func Activate(ctx context.Context, deps ActivationDeps, cfg config.RaftConfig, swap *SwappableService) (*Activation, error) {
	if swap == nil {
		return nil, fmt.Errorf("raft activate: SwappableService is required")
	}
	if !cfg.Enabled {
		return nil, fmt.Errorf("raft activate: cfg.Enabled is false")
	}
	if cfg.ClusterID == "" {
		return nil, fmt.Errorf("raft activate: RAFT_CLUSTER_ID is required")
	}

	// Reject re-activation. Subsequent runtime config updates (e.g. bridge
	// secret rotation) are handled by a separate path that does not rebuild
	// the Node itself.
	if swap.Enabled() {
		return nil, fmt.Errorf("raft activate: layer is already active; restart to reconfigure node-level settings")
	}

	fsm := NewFSM(deps.Logger)
	RegisterAppliers(fsm, deps.Appliers)
	fsm.SetSnapshotter(NewSQLiteSnapshotter(deps.DB))
	fsm.SetRestorer(NewSQLiteRestorer(deps.DB))

	node := NewNode(deps.Logger, cfg, fsm)
	if err := node.Start(ctx); err != nil {
		return nil, fmt.Errorf("raft activate: start node: %w", err)
	}

	swap.Swap(node)
	if deps.Logger != nil {
		deps.Logger.Info("raft: layer activated at runtime",
			"cluster_id", cfg.ClusterID,
			"node_id", cfg.NodeID,
			"bootstrap", cfg.Bootstrap,
			"advertise", firstNonEmptyAddr(cfg.AdvertiseAddr, cfg.BindAddr),
		)
	}
	return &Activation{Node: node, FSM: fsm}, nil
}

func firstNonEmptyAddr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
