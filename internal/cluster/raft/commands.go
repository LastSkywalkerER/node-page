// Package raft implements the cross-cluster state-replication layer for node-stats.
//
// Two independent Raft clusters (typically "local" LAN nodes and "public" VPS nodes)
// each replicate full state via the HashiCorp Raft library. An asynchronous bridge
// streams FSM-applied log entries from one cluster's leader to the other so both
// sides converge to the same materialised view of hosts, metrics, users and config.
//
// This file declares the small set of public types — the bigger pieces (Service,
// FSM, Node, bridge) live in their own files.
package raft

import (
	"context"
	"errors"
	"time"
)

// CommandType identifies the kind of mutation a Raft log entry encodes.
//
// Adding a new type is backwards compatible as long as the numeric value is not
// reused — older nodes will refuse to apply unknown types and log a warning.
type CommandType uint16

const (
	CmdUnknown CommandType = 0

	// Host registry
	CmdHostUpsert   CommandType = 10
	CmdHostDelete   CommandType = 11
	CmdHostLastSeen CommandType = 12
	// CmdConnectorHostUpsert feeds connector-discovered hosts (hypervisor
	// nodes / agent-less guests) incl. topology fields.
	CmdConnectorHostUpsert CommandType = 13

	// Metrics
	CmdMetricBatch           CommandType = 20
	CmdRetentionDeleteBefore CommandType = 21

	// Peer / URL catalog
	CmdPeerNodeAdvertise CommandType = 30
	CmdPeerNodeRemove    CommandType = 31

	// Auth (replicated user state)
	CmdUserUpsert         CommandType = 40
	CmdUserDelete         CommandType = 41
	CmdRefreshTokenIssue  CommandType = 42
	CmdRefreshTokenRevoke CommandType = 43
	CmdAuthSecretSet      CommandType = 44

	// Cluster-wide configuration
	CmdConfigSet CommandType = 50

	// Bridge bookkeeping
	CmdBridgeAck CommandType = 60

	// Bootstrap / join
	CmdJoinTokenIssue   CommandType = 70
	CmdJoinTokenConsume CommandType = 71

	// Connectors (external data sources, e.g. the Proxmox API)
	CmdConnectorUpsert CommandType = 80
	CmdConnectorDelete CommandType = 81
)

// Command is the envelope persisted in every Raft log entry.
//
// Payload is opaque to the consensus layer; FSM.Apply decodes it based on Type.
// All time-dependent state must be derived from Timestamp (leader-stamped),
// never from a wall clock inside Apply, so every replica produces identical
// SQLite content.
type Command struct {
	Type            CommandType `json:"type"`
	OriginClusterID string      `json:"origin_cluster_id,omitempty"`
	OriginNodeID    string      `json:"origin_node_id,omitempty"`
	OriginIndex     uint64      `json:"origin_index,omitempty"`
	Timestamp       time.Time   `json:"ts"`
	Payload         []byte      `json:"payload,omitempty"`
}

// SubmitResult is returned by Service.SubmitCommand for the local caller.
//
// It carries the index Raft assigned to the entry and any user-facing error
// produced by the FSM. Deterministic FSM errors are returned here but do NOT
// stop the log; non-deterministic errors (disk full, ctx cancel) panic and
// crash the node so it can rejoin from snapshot.
type SubmitResult struct {
	Index   uint64
	Applied bool
	Err     error
}

// SubmitResultWire is the JSON-safe form of SubmitResult, used when a
// follower forwards a command to the leader over HTTP. SubmitResult.Err is
// an `error` interface, which cannot round-trip through JSON (it marshals to
// {} and then fails to unmarshal back into the interface), so the error is
// carried here as a plain string.
type SubmitResultWire struct {
	Index   uint64 `json:"index"`
	Applied bool   `json:"applied"`
	Err     string `json:"err,omitempty"`
}

// ToWire converts a SubmitResult into its JSON-safe wire form.
func (r SubmitResult) ToWire() SubmitResultWire {
	w := SubmitResultWire{Index: r.Index, Applied: r.Applied}
	if r.Err != nil {
		w.Err = r.Err.Error()
	}
	return w
}

// SubmitResult reconstructs a SubmitResult from its wire form. The error
// loses its concrete type (becomes a plain errors.New); callers across the
// forward boundary only log the deterministic FSM error, so preserving the
// message is sufficient.
func (w SubmitResultWire) SubmitResult() SubmitResult {
	r := SubmitResult{Index: w.Index, Applied: w.Applied}
	if w.Err != "" {
		r.Err = errors.New(w.Err)
	}
	return r
}

// Status describes the local view of the Raft layer at a point in time.
type Status struct {
	Enabled         bool      `json:"enabled"`
	ClusterID       string    `json:"cluster_id,omitempty"`
	NodeID          string    `json:"node_id,omitempty"`
	State           string    `json:"state,omitempty"` // leader | follower | candidate | shutdown
	LeaderID        string    `json:"leader_id,omitempty"`
	LeaderAddr      string    `json:"leader_addr,omitempty"`
	Term            uint64    `json:"term,omitempty"`
	LastIndex       uint64    `json:"last_index,omitempty"`
	AppliedIndex    uint64    `json:"applied_index,omitempty"`
	CommitIndex     uint64    `json:"commit_index,omitempty"`
	Peers           []Peer    `json:"peers,omitempty"`
	AdvertiseAddr   string    `json:"advertise_addr,omitempty"`
	AdvertiseURL    string    `json:"advertise_url,omitempty"`
	BridgeEnabled   bool      `json:"bridge_enabled"`
	BridgeStartedAt time.Time `json:"bridge_started_at,omitempty"`
}

// Peer describes a Raft voter known to the local node.
type Peer struct {
	ID       string `json:"id"`
	Addr     string `json:"addr"`
	Suffrage string `json:"suffrage"` // voter | nonvoter
}

// raftApplierKey marks a context as originating from FSM.Apply. Repository
// writers check the WriteGate against this key when Raft is enabled, so that
// only the applier goroutine is allowed to touch state tables directly — every
// other caller must go through SubmitCommand.
type contextKey struct{}

var raftApplierKey = contextKey{}

// WithApplier returns a context flagged as coming from the FSM applier.
func WithApplier(ctx context.Context) context.Context {
	return context.WithValue(ctx, raftApplierKey, true)
}

// IsApplier reports whether ctx was produced by WithApplier.
func IsApplier(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	v, _ := ctx.Value(raftApplierKey).(bool)
	return v
}
