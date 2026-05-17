package raft

import (
	"context"
	"fmt"

	"github.com/charmbracelet/log"

	"system-stats/internal/app/config"
)

// New constructs the appropriate Service for the given configuration.
//
// When cfg.Enabled is false a DisabledService is returned and no resources
// are allocated — the legacy direct-write path keeps working. When enabled
// a Node is constructed and Start()ed (binding the transport and either
// bootstrapping or joining the cluster). The returned FSM can have
// CommandAppliers / Snapshotter / Restorer registered before or after this
// call as long as registration completes before the first log entry is
// applied (in practice that means before the leader starts replicating).
func New(ctx context.Context, logger *log.Logger, cfg config.RaftConfig) (Service, *FSM, error) {
	if !cfg.Enabled {
		return NewDisabledService(), nil, nil
	}
	if cfg.ClusterID == "" {
		return nil, nil, fmt.Errorf("raft: RAFT_CLUSTER_ID must be set when RAFT_ENABLED=true")
	}
	fsm := NewFSM(logger)
	node := NewNode(logger, cfg, fsm)
	if err := node.Start(ctx); err != nil {
		return nil, nil, err
	}
	return node, fsm, nil
}
