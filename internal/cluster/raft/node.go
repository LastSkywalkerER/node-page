package raft

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/charmbracelet/log"
	hraft "github.com/hashicorp/raft"
	raftboltdb "github.com/hashicorp/raft-boltdb/v2"

	"system-stats/internal/app/config"
)

// Node owns a HashiCorp Raft instance, its log/snapshot stores and transport.
// It implements Service so the rest of the app can submit Commands without
// importing the hashicorp library directly.
type Node struct {
	logger *log.Logger
	cfg    config.RaftConfig
	fsm    *FSM

	raft      *hraft.Raft
	transport hraft.Transport
	logStore  hraft.LogStore
	stable    hraft.StableStore
	snaps     hraft.SnapshotStore

	closeMu sync.Mutex
	closed  bool
}

// NewNode constructs (but does not start) a Raft node. Call Start to actually
// open stores, bind the transport and bootstrap or join the cluster.
func NewNode(logger *log.Logger, cfg config.RaftConfig, fsm *FSM) *Node {
	return &Node{logger: logger, cfg: cfg, fsm: fsm}
}

// Start initialises the BoltDB log/stable store, the file snapshot store and
// the TCP transport, then either bootstraps a brand-new cluster (when
// cfg.Bootstrap is true and there are no existing log entries) or joins the
// existing one using cfg.Peers.
func (n *Node) Start(ctx context.Context) error {
	if n.cfg.NodeID == "" {
		return fmt.Errorf("raft: RAFT_NODE_ID is required when RAFT_ENABLED=true")
	}
	if n.cfg.BindAddr == "" {
		return fmt.Errorf("raft: RAFT_BIND_ADDR is required when RAFT_ENABLED=true")
	}

	if err := os.MkdirAll(n.cfg.DataDir, 0o755); err != nil {
		return fmt.Errorf("raft: create data dir: %w", err)
	}

	logStorePath := filepath.Join(n.cfg.DataDir, "raft-log.bolt")
	stableStorePath := filepath.Join(n.cfg.DataDir, "raft-stable.bolt")
	snapshotsPath := filepath.Join(n.cfg.DataDir, "snapshots")

	logStore, err := raftboltdb.New(raftboltdb.Options{Path: logStorePath})
	if err != nil {
		return fmt.Errorf("raft: open log store: %w", err)
	}
	n.logStore = logStore

	stableStore, err := raftboltdb.New(raftboltdb.Options{Path: stableStorePath})
	if err != nil {
		_ = logStore.Close()
		return fmt.Errorf("raft: open stable store: %w", err)
	}
	n.stable = stableStore

	snaps, err := hraft.NewFileSnapshotStore(snapshotsPath, 3, os.Stderr)
	if err != nil {
		return fmt.Errorf("raft: open snapshot store: %w", err)
	}
	n.snaps = snaps

	advertise := n.cfg.AdvertiseAddr
	if advertise == "" {
		advertise = n.cfg.BindAddr
	}
	tcpAddr, err := net.ResolveTCPAddr("tcp", advertise)
	if err != nil {
		return fmt.Errorf("raft: resolve advertise addr %q: %w", advertise, err)
	}
	transport, err := hraft.NewTCPTransport(n.cfg.BindAddr, tcpAddr, 3, 10*time.Second, os.Stderr)
	if err != nil {
		return fmt.Errorf("raft: open TCP transport on %q: %w", n.cfg.BindAddr, err)
	}
	n.transport = transport

	rcfg := hraft.DefaultConfig()
	rcfg.LocalID = hraft.ServerID(n.cfg.NodeID)
	rcfg.SnapshotInterval = 60 * time.Second
	rcfg.SnapshotThreshold = 10000
	// Suppress hashicorp's default stderr writer noise; route via charmbracelet
	// is a future improvement.

	r, err := hraft.NewRaft(rcfg, n.fsm, logStore, stableStore, snaps, transport)
	if err != nil {
		return fmt.Errorf("raft: construct node: %w", err)
	}
	n.raft = r

	if n.cfg.Bootstrap {
		existing, err := hraft.HasExistingState(logStore, stableStore, snaps)
		if err != nil {
			return fmt.Errorf("raft: check existing state: %w", err)
		}
		if !existing {
			servers := []hraft.Server{{
				ID:      hraft.ServerID(n.cfg.NodeID),
				Address: hraft.ServerAddress(advertise),
			}}
			for _, p := range n.cfg.Peers {
				servers = append(servers, hraft.Server{
					ID:      hraft.ServerID(p.ID),
					Address: hraft.ServerAddress(p.Addr),
				})
			}
			if f := r.BootstrapCluster(hraft.Configuration{Servers: servers}); f.Error() != nil {
				return fmt.Errorf("raft: bootstrap cluster: %w", f.Error())
			}
			n.logger.Info("raft: bootstrapped new cluster",
				"node_id", n.cfg.NodeID, "cluster_id", n.cfg.ClusterID,
				"peers", len(servers))
		} else {
			n.logger.Info("raft: existing state found, skipping bootstrap",
				"node_id", n.cfg.NodeID)
		}
	} else {
		n.logger.Info("raft: started in non-bootstrap mode; peers must add this node",
			"node_id", n.cfg.NodeID, "advertise", advertise)
	}

	return nil
}

// SubmitCommand implements Service. It encodes cmd, applies it through Raft
// (leader-only — followers return ErrNotLeader; cross-leader forwarding is a
// follow-up commit) and waits up to timeout for FSM acknowledgement.
func (n *Node) SubmitCommand(ctx context.Context, cmd Command, timeout time.Duration) (SubmitResult, error) {
	if n.raft == nil {
		return SubmitResult{}, ErrDisabled
	}
	if n.raft.State() != hraft.Leader {
		// Forwarding to the leader over HTTPS lands in a follow-up commit;
		// for now, fail closed so callers can retry against the leader.
		return SubmitResult{}, ErrNotLeader
	}
	if cmd.Timestamp.IsZero() {
		cmd.Timestamp = time.Now().UTC()
	}
	data, err := json.Marshal(cmd)
	if err != nil {
		return SubmitResult{}, fmt.Errorf("raft: encode command: %w", err)
	}

	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	fut := n.raft.Apply(data, timeout)
	if err := fut.Error(); err != nil {
		return SubmitResult{Index: fut.Index()}, fmt.Errorf("raft: apply: %w", err)
	}
	res := SubmitResult{Index: fut.Index(), Applied: true}
	if respErr, ok := fut.Response().(error); ok && respErr != nil {
		res.Err = respErr
	}
	return res, nil
}

// Status implements Service.
func (n *Node) Status() Status {
	st := Status{
		Enabled:      true,
		ClusterID:    n.cfg.ClusterID,
		NodeID:       n.cfg.NodeID,
		AdvertiseURL: n.cfg.AdvertiseURL,
		BridgeEnabled: n.cfg.Bridge.Enabled,
	}
	if n.raft == nil {
		st.State = "shutdown"
		return st
	}
	st.State = n.raft.State().String()
	st.LastIndex = n.raft.LastIndex()
	st.AppliedIndex = n.fsm.AppliedIndex()
	st.CommitIndex = n.raft.CommitIndex()
	if leaderAddr, leaderID := n.raft.LeaderWithID(); leaderID != "" {
		st.LeaderID = string(leaderID)
		st.LeaderAddr = string(leaderAddr)
	}
	stats := n.raft.Stats()
	if t, ok := stats["term"]; ok {
		// parse best-effort
		fmt.Sscanf(t, "%d", &st.Term)
	}
	cfgFut := n.raft.GetConfiguration()
	if err := cfgFut.Error(); err == nil {
		for _, s := range cfgFut.Configuration().Servers {
			suffrage := "voter"
			if s.Suffrage == hraft.Nonvoter {
				suffrage = "nonvoter"
			}
			st.Peers = append(st.Peers, Peer{
				ID:       string(s.ID),
				Addr:     string(s.Address),
				Suffrage: suffrage,
			})
		}
	}
	return st
}

// Enabled implements Service.
func (n *Node) Enabled() bool { return n.raft != nil }

// Close shuts the Raft node down and closes its stores. Safe to call multiple
// times.
func (n *Node) Close() error {
	n.closeMu.Lock()
	defer n.closeMu.Unlock()
	if n.closed {
		return nil
	}
	n.closed = true

	var firstErr error
	if n.raft != nil {
		if err := n.raft.Shutdown().Error(); err != nil {
			firstErr = err
		}
	}
	if closer, ok := n.logStore.(interface{ Close() error }); ok {
		if err := closer.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if closer, ok := n.stable.(interface{ Close() error }); ok {
		if err := closer.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
