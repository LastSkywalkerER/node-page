package raft

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/charmbracelet/log"
	"github.com/hashicorp/go-hclog"
	hraft "github.com/hashicorp/raft"
	raftboltdb "github.com/hashicorp/raft-boltdb/v2"
	raftwal "github.com/hashicorp/raft-wal"
	"gorm.io/gorm"

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

	// db is set after construction so SubmitCommand on a follower can
	// look up the leader's HTTP URL in peer_node_advertise and forward
	// the command via /raft/forward.
	db *gorm.DB

	closeMu sync.Mutex
	closed  bool

	// forwardSecretFn returns the current cluster-shared secret used to sign
	// follower→leader command forwards (see forwardauth.go). Set after
	// construction; nil leaves forwards unsigned (rejected by an
	// HMAC-enforcing leader — so the provider must be wired for forwarding to
	// work at all once Raft is active).
	forwardSecretMu sync.RWMutex
	forwardSecretFn func() string

	// Cached result of the leader-reachability probe (see leaderReachable).
	// Status() is polled every ~5s by the admin UI, so the actual TCP dial
	// is rate-limited to leaderProbeTTL to keep the status handler cheap.
	leaderProbeMu   sync.Mutex
	leaderProbeAt   time.Time
	leaderProbeAddr string
	leaderProbeOK   bool
}

// leaderProbeTTL bounds how often Status() performs the leader-reachability
// TCP dial. Long enough that a 5s poll doesn't dial every time, short enough
// that the recovery banner appears within a few seconds of a partition.
const leaderProbeTTL = 4 * time.Second

// SetDB wires the GORM handle the leader-forwarder uses to look up peer
// URLs. Safe to call after construction; must be called before
// SubmitCommand is used on a follower.
func (n *Node) SetDB(db *gorm.DB) { n.db = db }

// SetForwardSecretProvider wires the source of the cluster-shared secret used
// to HMAC-sign follower→leader command forwards. The provider is read on every
// forward so a live secret swap (a joined node discovering the real cluster
// key) takes effect without rebuilding the node.
func (n *Node) SetForwardSecretProvider(fn func() string) {
	n.forwardSecretMu.Lock()
	n.forwardSecretFn = fn
	n.forwardSecretMu.Unlock()
}

// forwardSecret returns the current forward-signing secret, or "".
func (n *Node) forwardSecret() string {
	n.forwardSecretMu.RLock()
	fn := n.forwardSecretFn
	n.forwardSecretMu.RUnlock()
	if fn == nil {
		return ""
	}
	return fn()
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
//
// Any failure path is responsible for releasing already-acquired resources
// (BoltDB file locks, TCP listener) so retrying Start() from the wizard
// after a transient error (port in use, permission denied) does not hang
// on a leaked flock.
func (n *Node) Start(ctx context.Context) error {
	if n.cfg.NodeID == "" {
		return fmt.Errorf("raft: RAFT_NODE_ID is required when RAFT_ENABLED=true")
	}
	if n.cfg.BindAddr == "" {
		return fmt.Errorf("raft: RAFT_BIND_ADDR is required when RAFT_ENABLED=true")
	}

	if err := os.MkdirAll(n.cfg.DataDir, 0o755); err != nil {
		cwd, _ := os.Getwd()
		return fmt.Errorf("raft: create data dir %q (cwd %q): %w", n.cfg.DataDir, cwd, err)
	}

	logStoreDir := filepath.Join(n.cfg.DataDir, "raft-log-wal")
	stableStorePath := filepath.Join(n.cfg.DataDir, "raft-stable.bolt")
	snapshotsPath := filepath.Join(n.cfg.DataDir, "snapshots")

	// Track resources so we can release them all on any failure path.
	var (
		logStore    hraft.LogStore
		stableStore hraft.StableStore
		transport   hraft.Transport
		raftNode    *hraft.Raft
		ok          bool
	)
	defer func() {
		if ok {
			return
		}
		// Best-effort cleanup in reverse order; ignore errors because we
		// are already on a failure path.
		if raftNode != nil {
			_ = raftNode.Shutdown().Error()
		}
		if closer, c := transport.(interface{ Close() error }); c && closer != nil {
			_ = closer.Close()
		}
		if closer, c := stableStore.(interface{ Close() error }); c && closer != nil {
			_ = closer.Close()
		}
		if closer, c := logStore.(interface{ Close() error }); c && closer != nil {
			_ = closer.Close()
		}
	}()

	// Log store: raft-wal (append-only segment files), NOT raft-boltdb. BoltDB
	// mmap's its whole single file and never shrinks it, so the metric-batch log
	// ratcheted RSS up in power-of-2 steps overnight and never released it
	// (file-backed mmap, invisible to GC/GOMEMLIMIT/FreeOSMemory). raft-wal reads
	// via pread (no mmap of log data) and rotates+deletes old segment files, so
	// the on-disk + resident footprint tracks the live trailing window and
	// actually recedes when load drops. The data dir must be a fresh directory.
	if err := os.MkdirAll(logStoreDir, 0o755); err != nil {
		return fmt.Errorf("raft: create log store dir %q: %w", logStoreDir, err)
	}
	walLog, err := raftwal.Open(logStoreDir,
		// raft-wal preallocates each segment file to the segment size on disk
		// (not mmap'd — reads are pread, so it costs disk, not RSS). The default
		// is 64 MiB; since metrics no longer flow through the log, our log carries
		// only low-rate control-plane commands (host upserts, config), so a much
		// smaller segment keeps the on-disk footprint tidy while still dwarfing
		// any single entry. Old segments are rotated out and deleted.
		raftwal.WithSegmentSize(16*1024*1024),
		raftwal.WithLogger(hclog.New(&hclog.LoggerOptions{
			Name:   "raft-wal",
			Level:  hclog.Warn,
			Output: os.Stderr,
		})))
	if err != nil {
		return fmt.Errorf("raft: open WAL log store: %w", err)
	}
	logStore = walLog
	n.logStore = walLog

	sst, err := raftboltdb.New(raftboltdb.Options{Path: stableStorePath})
	if err != nil {
		return fmt.Errorf("raft: open stable store: %w", err)
	}
	stableStore = sst
	n.stable = sst

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
	// Use a custom TCP listener with SO_REUSEADDR (+ SO_REUSEPORT on
	// Linux/Darwin) so an unclean previous-process exit (e.g. SIGKILL
	// from air during hot reload) doesn't leave the port wedged.
	tr, _, err := newReuseAddrTCPTransport(n.cfg.BindAddr, tcpAddr, 3, 10*time.Second)
	if err != nil {
		return fmt.Errorf("raft: open TCP transport (bind=%q advertise=%q): %w", n.cfg.BindAddr, advertise, err)
	}
	transport = tr
	n.transport = tr

	rcfg := hraft.DefaultConfig()
	rcfg.LocalID = hraft.ServerID(n.cfg.NodeID)
	// Snapshot OFTEN so the BoltDB raft log is truncated frequently and a
	// restart never replays a huge accumulated metric backlog through the
	// single FSM goroutine (the "raft: apply: timed out enqueuing operation"
	// storm that wedged the node for minutes). Snapshots now EXCLUDE the metric
	// history tables (see snapshot_sqlite.go managedTables), so a snapshot is
	// cheap — it no longer dumps the bulk of the DB under one transaction — and
	// frequent compaction is the cheaper trade-off. Every CmdMetricBatch is
	// still appended to the durable log between snapshots, so keeping the
	// threshold low bounds how many of those entries can pile up.
	rcfg.SnapshotInterval = 90 * time.Second
	rcfg.SnapshotThreshold = 4096
	// Keep only a small tail of log entries after each snapshot. A large
	// trailing window is what lets the metric-batch entries accumulate (and get
	// replayed on restart); a tight bound keeps the on-disk log small.
	rcfg.TrailingLogs = 8192

	r, err := hraft.NewRaft(rcfg, n.fsm, logStore, stableStore, snaps, transport)
	if err != nil {
		return fmt.Errorf("raft: construct node: %w", err)
	}
	raftNode = r
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

	ok = true
	return nil
}

// SubmitCommand implements Service. It encodes cmd, applies it through Raft
// (leader-only — followers return ErrNotLeader; cross-leader forwarding is a
// follow-up commit) and waits up to timeout for FSM acknowledgement.
func (n *Node) SubmitCommand(ctx context.Context, cmd Command, timeout time.Duration) (SubmitResult, error) {
	if n.raft == nil {
		return SubmitResult{}, ErrDisabled
	}
	if cmd.Timestamp.IsZero() {
		cmd.Timestamp = time.Now().UTC()
	}
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	if n.raft.State() != hraft.Leader {
		// Forward over HTTP to the current leader. The leader's URL is
		// discovered via the replicated peer_node_advertise table —
		// every node publishes its HTTP URL there on activation, and
		// the leader publishes for the joiner on /raft/join.
		return n.forwardToLeader(ctx, cmd, timeout)
	}
	data, err := json.Marshal(cmd)
	if err != nil {
		return SubmitResult{}, fmt.Errorf("raft: encode command: %w", err)
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

// forwardToLeader looks up the current leader's HTTP URL in the
// replicated peer_node_advertise table and POSTs the command to its
// /api/v1/raft/forward endpoint. Returns ErrNotLeader when no leader
// is known or no URL is published for it.
func (n *Node) forwardToLeader(ctx context.Context, cmd Command, timeout time.Duration) (SubmitResult, error) {
	if n.db == nil {
		return SubmitResult{}, ErrNotLeader
	}
	_, leaderID := n.raft.LeaderWithID()
	if leaderID == "" {
		return SubmitResult{}, ErrNotLeader
	}
	lookupCtx, lookupCancel := context.WithTimeout(ctx, 2*time.Second)
	url, err := LookupPeerURL(lookupCtx, n.db, n.cfg.ClusterID, string(leaderID))
	lookupCancel()
	if err != nil {
		return SubmitResult{}, fmt.Errorf("raft: lookup leader URL: %w", err)
	}
	if url == "" {
		return SubmitResult{}, fmt.Errorf("%w: leader %q has not published its HTTP URL yet (peer_node_advertise empty)", ErrNotLeader, leaderID)
	}
	body, err := json.Marshal(cmd)
	if err != nil {
		return SubmitResult{}, fmt.Errorf("raft: encode forwarded command: %w", err)
	}
	postCtx, postCancel := context.WithTimeout(ctx, timeout+2*time.Second)
	defer postCancel()
	req, err := http.NewRequestWithContext(postCtx, http.MethodPost, url+"/api/v1/raft/forward", bytes.NewReader(body))
	if err != nil {
		return SubmitResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	// Authenticate the forward: sign the body with the cluster-shared secret so
	// the leader only applies commands from genuine cluster peers, not from
	// anyone who can reach its HTTP port.
	ts := time.Now().UnixNano()
	req.Header.Set(ForwardTimestampHeader, strconv.FormatInt(ts, 10))
	req.Header.Set(ForwardClusterHeader, n.cfg.ClusterID)
	req.Header.Set(ForwardSignatureHeader, signForward(n.forwardSecret(), ts, body))
	resp, err := (&http.Client{Timeout: timeout + 2*time.Second}).Do(req)
	if err != nil {
		return SubmitResult{}, fmt.Errorf("raft: forward to %s: %w", url, err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		return SubmitResult{}, fmt.Errorf("raft: leader returned %s: %s", resp.Status, string(respBody))
	}
	var wire SubmitResultWire
	if err := json.Unmarshal(respBody, &wire); err != nil {
		return SubmitResult{}, fmt.Errorf("raft: decode forwarded response: %w", err)
	}
	return wire.SubmitResult(), nil
}

// Stats returns the raw hashicorp/raft Stats map. Useful for the admin
// UI to surface low-level fields (last_contact, num_peers, term, etc.)
// when diagnosing a wedged cluster.
func (n *Node) Stats() map[string]string {
	if n.raft == nil {
		return nil
	}
	return n.raft.Stats()
}

// Status implements Service.
func (n *Node) Status() Status {
	st := Status{
		Enabled:       true,
		ClusterID:     n.cfg.ClusterID,
		NodeID:        n.cfg.NodeID,
		AdvertiseAddr: n.cfg.AdvertiseAddr,
		AdvertiseURL:  n.cfg.AdvertiseURL,
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

	// Probe leader reachability only when we are a non-leader with a known
	// leader. The address comes from the configuration we just built (the
	// leader's advertised Raft addr). This is what distinguishes a genuinely
	// healthy follower from one wedged behind a one-way partition.
	if st.State != hraft.Leader.String() && st.LeaderID != "" && st.LeaderID != n.cfg.NodeID {
		leaderAddr := st.LeaderAddr
		if leaderAddr == "" {
			for _, p := range st.Peers {
				if p.ID == st.LeaderID {
					leaderAddr = p.Addr
					break
				}
			}
		}
		if leaderAddr != "" {
			ok := n.leaderReachable(leaderAddr)
			st.LeaderReachable = &ok
		}
	}
	return st
}

// leaderReachable reports whether a fresh TCP connection to the leader's
// advertised Raft address succeeds, caching the result for leaderProbeTTL so
// the polled Status() handler doesn't dial on every call. A failing dial while
// the node still shows as a follower is the signature of an asymmetric
// partition (the wedged-for-writes state the recovery UI must surface).
func (n *Node) leaderReachable(addr string) bool {
	n.leaderProbeMu.Lock()
	defer n.leaderProbeMu.Unlock()
	if addr == n.leaderProbeAddr && time.Since(n.leaderProbeAt) < leaderProbeTTL {
		return n.leaderProbeOK
	}
	conn, err := net.DialTimeout("tcp", addr, 1500*time.Millisecond)
	ok := err == nil
	if conn != nil {
		_ = conn.Close()
	}
	n.leaderProbeAddr = addr
	n.leaderProbeAt = time.Now()
	n.leaderProbeOK = ok
	return ok
}

// Enabled implements Service.
func (n *Node) Enabled() bool { return n.raft != nil }

// AddVoter adds a peer Raft voter. Only valid when this node is the leader;
// otherwise the underlying library returns ErrNotLeader.
func (n *Node) AddVoter(id, addr string) error {
	if n.raft == nil {
		return ErrDisabled
	}
	f := n.raft.AddVoter(hraft.ServerID(id), hraft.ServerAddress(addr), 0, 10*time.Second)
	if err := f.Error(); err != nil {
		return fmt.Errorf("raft: add voter: %w", err)
	}
	return nil
}

// AddNonvoter adds a peer as a non-voting member. A non-voter receives the
// full log + snapshots (so it converges and can serve reads) but does NOT
// count toward quorum — so adding one can never reduce the cluster's write
// availability. New nodes join this way and are promoted to voter only once
// the leader confirms they stay reachable. Leader-only.
func (n *Node) AddNonvoter(id, addr string) error {
	if n.raft == nil {
		return ErrDisabled
	}
	f := n.raft.AddNonvoter(hraft.ServerID(id), hraft.ServerAddress(addr), 0, 10*time.Second)
	if err := f.Error(); err != nil {
		return fmt.Errorf("raft: add nonvoter: %w", err)
	}
	return nil
}

// DemoteVoter turns a voting member back into a non-voter so it stops counting
// toward quorum (used when a voter has been unreachable long enough that it is
// dragging down write availability). Leader-only; the config change still needs
// the CURRENT quorum to commit, so the caller must only attempt it while a
// majority of voters is reachable.
func (n *Node) DemoteVoter(id string) error {
	if n.raft == nil {
		return ErrDisabled
	}
	f := n.raft.DemoteVoter(hraft.ServerID(id), 0, 10*time.Second)
	if err := f.Error(); err != nil {
		return fmt.Errorf("raft: demote voter: %w", err)
	}
	return nil
}

// RemovePeer removes a peer from the Raft configuration. Leader-only.
func (n *Node) RemovePeer(id string) error {
	if n.raft == nil {
		return ErrDisabled
	}
	f := n.raft.RemoveServer(hraft.ServerID(id), 0, 10*time.Second)
	if err := f.Error(); err != nil {
		return fmt.Errorf("raft: remove server: %w", err)
	}
	return nil
}

// IsLeader reports whether the local node currently holds Raft leadership.
func (n *Node) IsLeader() bool {
	return n.raft != nil && n.raft.State() == hraft.Leader
}

// TransferLeadership hands leadership to a healthy follower (hashicorp/raft picks
// the most up-to-date one) and blocks until the transfer completes. Returns an
// error if this node isn't the leader or no eligible follower is reachable.
func (n *Node) TransferLeadership() error {
	if n.raft == nil {
		return ErrDisabled
	}
	if err := n.raft.LeadershipTransfer().Error(); err != nil {
		return fmt.Errorf("raft: transfer leadership: %w", err)
	}
	return nil
}

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
