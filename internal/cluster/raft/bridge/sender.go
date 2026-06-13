package bridge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/log"

	raftcluster "system-stats/internal/cluster/raft"
)

// Envelope is the wire shape of a single replicated entry. It's a compact
// projection of raftcluster.ApplyEvent so the receiver can reconstruct a
// raftcluster.Command and submit it locally.
type Envelope struct {
	OriginIndex     uint64                  `json:"origin_index"`
	OriginClusterID string                  `json:"origin_cluster_id"`
	OriginNodeID    string                  `json:"origin_node_id"`
	Timestamp       time.Time               `json:"ts"`
	Type            raftcluster.CommandType `json:"type"`
	Payload         []byte                  `json:"payload"`
}

// Batch is the wire shape of one /raft/bridge/replicate POST body.
type Batch struct {
	Entries []Envelope `json:"entries"`
}

// Sender ships locally-applied FSM entries to the peer cluster's leader.
// It only ships when this node currently holds Raft leadership; followers
// drain the apply-event channel and discard.
type Sender struct {
	logger    *log.Logger
	svc       raftcluster.Service
	events    <-chan raftcluster.ApplyEvent
	picker    *Picker
	secret    string
	myCluster string
	myNode    string

	flushEvery time.Duration
	flushSize  int

	httpClient *http.Client

	// uplinkOnly restricts shipping to the host/metric command set (bridge
	// mode "push"): a spoke must never export users, auth secrets or cluster
	// config to the hub — different clusters keep independent identities.
	uplinkOnly bool
	// reconcile re-submits the local control-plane state into the local log
	// (BackfillLocalHosts) so the hub heals from any gap the live stream
	// dropped (FSM event-channel overflow, hub downtime past the buffer cap).
	// Called on a slow cadence and after a ship failure→success transition.
	reconcile func(ctx context.Context)

	mu           sync.Mutex
	lastShipAt   time.Time
	lastShipErr  string
	shippedTotal uint64
	pending      int
	failing      bool
}

const (
	// backoffBase is the first backoff after a ship failure; it doubles each
	// consecutive failure (1s, 2s, 4s …) up to backoffMax. Jitter (±25%) is
	// applied so multiple spokes recovering after a hub blip don't synchronise
	// their retries into a thundering herd against a 2-CPU/4GB VPS.
	backoffBase = 1 * time.Second
	backoffMax  = 60 * time.Second
)

// uplinkTypes is the allowlist shipped in "push" mode: host registry,
// topology and metrics — nothing identity- or config-bearing.
var uplinkTypes = map[raftcluster.CommandType]bool{
	raftcluster.CmdHostUpsert:          true,
	raftcluster.CmdConnectorHostUpsert: true,
	raftcluster.CmdHostDelete:          true,
	raftcluster.CmdHostLastSeen:        true,
	raftcluster.CmdMetricBatch:         true,
}

// crossClusterDeny lists command types that must NEVER cross the HTTPS bridge
// in ANY mode (push or both). Connector rows carry secret_enc encrypted under a
// key derived from the LOCAL cluster's JWT secret (connectors.NewCipher); the
// peer cluster keeps an independent identity/secret and could neither decrypt
// the ciphertext nor should it ever receive another cluster's credentials. The
// in-cluster Raft replication of these commands is untouched — this only gates
// what the bridge sender ships off-cluster.
var crossClusterDeny = map[raftcluster.CommandType]bool{
	raftcluster.CmdConnectorUpsert: true,
	raftcluster.CmdConnectorDelete: true,
}

// shouldShip decides whether a locally-applied command is exported over the
// cross-cluster bridge. It encodes, in order: don't echo peer-origin entries
// back; drop bridge bookkeeping; NEVER export connector secrets in any mode;
// and in uplink ("push") mode restrict to the host/metric allowlist.
func (s *Sender) shouldShip(cmd raftcluster.Command) bool {
	// Skip entries that originated in a peer cluster — those are the ones we
	// just received over the bridge, never ship back.
	if cmd.OriginClusterID != "" && cmd.OriginClusterID != s.myCluster {
		return false
	}
	// Skip bridge bookkeeping entries; the peer doesn't need them.
	if cmd.Type == raftcluster.CmdBridgeAck {
		return false
	}
	// Never ship connector secrets across the cluster boundary — they are
	// encrypted under this cluster's key and belong to no other cluster
	// (applies in every mode, push and both).
	if crossClusterDeny[cmd.Type] {
		return false
	}
	// Uplink mode exports hosts/metrics only — never identity state.
	if s.uplinkOnly && !uplinkTypes[cmd.Type] {
		return false
	}
	return true
}

// NewSender wires the sender.
func NewSender(logger *log.Logger, svc raftcluster.Service, events <-chan raftcluster.ApplyEvent, picker *Picker, secret, myClusterID, myNodeID string) *Sender {
	return &Sender{
		logger:     logger,
		svc:        svc,
		events:     events,
		picker:     picker,
		secret:     secret,
		myCluster:  myClusterID,
		myNode:     myNodeID,
		flushEvery: 250 * time.Millisecond,
		flushSize:  50,
		// No client-level Timeout: a ship is a large batch the hub applies
		// through its own Raft log (fsync per entry) and can legitimately take
		// tens of seconds. shipBatch governs the deadline with a per-request 90s
		// context — a 10s client cap here would abort mid-flight (it fires
		// whichever is sooner) and re-ship the same work forever.
		httpClient: &http.Client{},
	}
}

// WithUplinkOnly restricts shipping to the host/metric allowlist (mode push).
func (s *Sender) WithUplinkOnly(v bool) *Sender {
	s.uplinkOnly = v
	return s
}

// WithReconcile registers the periodic state-backfill callback.
func (s *Sender) WithReconcile(fn func(ctx context.Context)) *Sender {
	s.reconcile = fn
	return s
}

// Snapshot is the sender's health view for the admin UI.
type SenderSnapshot struct {
	LastShipAt   time.Time `json:"last_ship_at,omitempty"`
	LastShipErr  string    `json:"last_ship_err,omitempty"`
	ShippedTotal uint64    `json:"shipped_total"`
	Pending      int       `json:"pending"`
	UplinkOnly   bool      `json:"uplink_only"`
}

// Snapshot returns current sender stats.
func (s *Sender) Snapshot() SenderSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return SenderSnapshot{
		LastShipAt:   s.lastShipAt,
		LastShipErr:  s.lastShipErr,
		ShippedTotal: s.shippedTotal,
		Pending:      s.pending,
		UplinkOnly:   s.uplinkOnly,
	}
}

// Run drains the apply-event channel and ships batches to the peer
// cluster's leader (chosen by the URL Picker). Stops when ctx is done.
func (s *Sender) Run(ctx context.Context) {
	tick := time.NewTicker(s.flushEvery)
	defer tick.Stop()
	// Reconcile cadence: heals hub-side gaps from dropped events / downtime.
	recTick := time.NewTicker(10 * time.Minute)
	defer recTick.Stop()
	var buf []Envelope
	var lastWarnMsg string
	var lastWarnAt time.Time
	// Exponential backoff on consecutive ship failures: while backing off the
	// loop keeps ACCEPTING/buffering entries (the drop-oldest cap still
	// applies) but does not attempt a ship until backoffUntil passes. Reset to
	// zero on the first success.
	var backoff time.Duration
	var backoffUntil time.Time
	var droppedTotal uint64

	runReconcile := func() {
		if s.reconcile != nil && s.svc.IsLeader() {
			go s.reconcile(ctx)
		}
	}

	// capBuffer enforces the drop-oldest bound. It returns how many entries it
	// dropped so the caller can account for the previously-silent loss.
	capBuffer := func() {
		max := s.flushSize * 4
		if len(buf) > max {
			drop := len(buf) - max
			buf = buf[drop:]
			droppedTotal += uint64(drop)
			if s.logger != nil {
				s.logger.Warn("bridge: dropped oldest buffered entries (cap reached)",
					"dropped", drop, "dropped_total", droppedTotal, "cap", max)
			}
		}
	}

	flush := func() {
		s.mu.Lock()
		s.pending = len(buf)
		s.mu.Unlock()
		if len(buf) == 0 {
			return
		}
		// Only the leader ships. Followers drained their buffer earlier
		// but we re-check here in case state changed mid-tick.
		if !s.svc.IsLeader() {
			buf = buf[:0]
			return
		}
		// Backing off after a failure: do not ship yet, but keep the buffer
		// (it is still being filled by the event case) bounded.
		if time.Now().Before(backoffUntil) {
			capBuffer()
			return
		}
		if err := s.shipBatch(ctx, Batch{Entries: buf}); err != nil {
			backoff = nextBackoff(backoff)
			backoffUntil = time.Now().Add(backoff)
			// flush runs every few hundred ms — under a persistent failure
			// repeat the warning only when the message changes or each 30 s.
			if s.logger != nil && (err.Error() != lastWarnMsg || time.Since(lastWarnAt) > 30*time.Second) {
				s.logger.Warn("bridge: ship batch failed", "error", err, "entries", len(buf), "backoff", backoff.Round(time.Millisecond))
				lastWarnMsg = err.Error()
				lastWarnAt = time.Now()
			}
			s.mu.Lock()
			s.lastShipErr = err.Error()
			s.failing = true
			s.mu.Unlock()
			// Keep the buffer so the next eligible tick retries. Cap so we
			// don't grow unbounded under sustained failure — drop oldest.
			capBuffer()
			return
		}
		backoff = 0
		backoffUntil = time.Time{}
		s.mu.Lock()
		s.lastShipAt = time.Now()
		s.lastShipErr = ""
		s.shippedTotal += uint64(len(buf))
		wasFailing := s.failing
		s.failing = false
		s.pending = 0
		s.mu.Unlock()
		buf = buf[:0]
		if wasFailing {
			// The peer just came back: entries past the buffer cap are gone
			// from the live stream — re-submit local state to close the gap.
			runReconcile()
		}
	}

	// Initial reconcile shortly after start so a fresh hub catches up with
	// existing spoke state without waiting for organic upserts.
	startupRec := time.AfterFunc(15*time.Second, runReconcile)
	defer startupRec.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-recTick.C:
			runReconcile()
		case ev := <-s.events:
			if !s.shouldShip(ev.Cmd) {
				continue
			}
			env := Envelope{
				OriginIndex:     ev.Index,
				OriginClusterID: firstNonEmpty(ev.Cmd.OriginClusterID, s.myCluster),
				OriginNodeID:    firstNonEmpty(ev.Cmd.OriginNodeID, s.myNode),
				Timestamp:       ev.Cmd.Timestamp,
				Type:            ev.Cmd.Type,
				Payload:         ev.Cmd.Payload,
			}
			buf = append(buf, env)
			if len(buf) >= s.flushSize {
				flush()
			}
		case <-tick.C:
			flush()
		}
	}
}

func (s *Sender) shipBatch(ctx context.Context, batch Batch) error {
	if s.picker == nil {
		return errors.New("bridge: no picker configured")
	}
	url := s.picker.Pick("")
	if url == "" {
		return errors.New("bridge: no healthy peer URL")
	}
	body, err := json.Marshal(batch)
	if err != nil {
		return fmt.Errorf("encode batch: %w", err)
	}
	tsNanos := time.Now().UnixNano()
	sig := Sign(s.secret, tsNanos, body)

	// Generous: the hub applies every entry through its own Raft log
	// (fsync per entry) — on modest disks a 200-entry batch takes tens of
	// seconds, and abandoning it early just re-ships the same work.
	reqCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, url+"/api/v1/raft/bridge/replicate", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(HMACHeader, sig)
	req.Header.Set(TimestampHeader, strconv.FormatInt(tsNanos, 10))
	req.Header.Set(SenderClusterHeader, s.myCluster)
	req.Header.Set(SenderNodeHeader, s.myNode)

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		// The receiver explains the rejection in the body ("sender cluster
		// id equals …", "invalid timestamp", …) — surface it, it is the
		// actionable part.
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		if msg := strings.TrimSpace(string(b)); msg != "" {
			return fmt.Errorf("peer returned %s: %s", resp.Status, msg)
		}
		return fmt.Errorf("peer returned %s", resp.Status)
	}
	return nil
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// nextBackoff doubles the current backoff (starting at backoffBase) capped at
// backoffMax, then applies ±25% jitter. A zero/negative current value seeds
// the first step at backoffBase.
func nextBackoff(current time.Duration) time.Duration {
	next := current * 2
	if current <= 0 {
		next = backoffBase
	}
	if next > backoffMax {
		next = backoffMax
	}
	// ±25% jitter so concurrent spokes don't retry in lock-step.
	jitter := time.Duration((rand.Float64() - 0.5) * 0.5 * float64(next))
	d := next + jitter
	if d < 0 {
		d = next
	}
	return d
}
