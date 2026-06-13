package bridge

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/charmbracelet/log"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	raftcluster "system-stats/internal/cluster/raft"
)

// Receiver is the HTTP handler for POST /api/v1/raft/bridge/replicate.
// It HMAC-verifies the request, dedupes each entry against
// applied_remote_log and submits the resulting raftcluster.Command into
// the local Raft log with OriginClusterID preserved. The Sender's loop-
// prevention rule (skip ev.Cmd.OriginClusterID != myCluster) means these
// entries never bounce back to the peer.
type Receiver struct {
	logger    *log.Logger
	svc       raftcluster.Service
	db        *gorm.DB
	secret    string
	myCluster string
	// uplinkOnly (bridge mode "receive"): defense in depth — even if a
	// misconfigured peer ships identity state (users, auth secrets, config),
	// the hub silently drops everything outside the host/metric allowlist.
	uplinkOnly bool

	// sem bounds concurrent SUBMIT-bearing replicate handling. A flood of
	// senders (many spokes, each retrying) otherwise piles onto the apply
	// pipeline while the FSM is jammed snapshotting, each handler blocking for
	// the apply timeout. When the slots are full the handler returns 503 at
	// once (sender retries with its backoff) instead of queueing more work.
	// Duplicate-only batches short-circuit to 200 BEFORE acquiring a slot.
	sem chan struct{}

	lastPruneUnix int64 // atomic; applied_remote_log GC bookkeeping
}

const (
	// replicateConcurrency caps in-flight submit-bearing replicate handlers.
	replicateConcurrency = 2
	// replicateApplyTimeout is the SHORT enqueue timeout used when submitting
	// fresh entries to Raft. When the FSM is busy (e.g. snapshotting) the apply
	// enqueue times out fast and the handler returns a retryable 503 rather than
	// blocking for tens of seconds and amplifying the sender's retry storm.
	replicateApplyTimeout = 5 * time.Second
)

// WithUplinkOnly restricts accepted commands to the host/metric allowlist.
func (r *Receiver) WithUplinkOnly(v bool) *Receiver {
	r.uplinkOnly = v
	return r
}

// NewReceiver wires the receiver.
func NewReceiver(logger *log.Logger, svc raftcluster.Service, db *gorm.DB, secret, myClusterID string) *Receiver {
	return &Receiver{
		logger:    logger,
		svc:       svc,
		db:        db,
		secret:    secret,
		myCluster: myClusterID,
		sem:       make(chan struct{}, replicateConcurrency),
	}
}

// Handle is the Gin handler. Mount under POST /api/v1/raft/bridge/replicate.
func (r *Receiver) Handle(c *gin.Context) {
	if r.svc == nil || !r.svc.Enabled() {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "raft disabled"})
		return
	}
	if !r.svc.IsLeader() {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "not the leader; retry on the cluster leader"})
		return
	}

	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "read body: " + err.Error()})
		return
	}
	tsStr := c.GetHeader(TimestampHeader)
	tsNanos, err := strconv.ParseInt(tsStr, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid timestamp header"})
		return
	}
	if err := Verify(r.secret, c.GetHeader(HMACHeader), tsNanos, body); err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}
	sender := c.GetHeader(SenderClusterHeader)
	if sender == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing sender cluster id header"})
		return
	}
	if sender == r.myCluster {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf(
			"sender cluster id %q equals this receiver's own RAFT_CLUSTER_ID — every site needs a unique id; change it on one side (on all of that site's nodes) and restart", sender)})
		return
	}

	var batch Batch
	if err := json.Unmarshal(body, &batch); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "decode batch: " + err.Error()})
		return
	}

	// Detached from the request context on purpose: on a slow hub a batch
	// can outlive the sender's HTTP timeout. If the client hangs up mid-way,
	// entries already submitted into Raft must still be recorded in the
	// dedupe log — otherwise the re-shipped batch re-applies them forever
	// (apply work done, MarkApplied "context canceled", repeat).
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	// Filter + dedupe first, then submit the survivors as ONE raft entry:
	// per-envelope SubmitCommand meant one fsync'd consensus round per
	// metric tick per host - the dominant resource cost of a busy hub.
	//
	// Dedupe with ONE query for the whole batch: a sender retry re-ships the
	// same indexes, so a per-entry SELECT count(*) was N queries per batch.
	// Indexes are scoped per origin cluster; build the lookup set per cluster.
	var deduped, skipped int
	indexesByOrigin := make(map[string][]uint64, 1)
	for _, env := range batch.Entries {
		if r.uplinkOnly && !uplinkTypes[env.Type] {
			continue
		}
		indexesByOrigin[env.OriginClusterID] = append(indexesByOrigin[env.OriginClusterID], env.OriginIndex)
	}
	appliedByOrigin := make(map[string]map[uint64]struct{}, len(indexesByOrigin))
	for origin, indexes := range indexesByOrigin {
		set, err := AppliedIndexSet(ctx, r.db, origin, indexes)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "dedupe check: " + err.Error()})
			return
		}
		appliedByOrigin[origin] = set
	}

	fresh := make([]raftcluster.BridgeEnvelope, 0, len(batch.Entries))
	for _, env := range batch.Entries {
		if r.uplinkOnly && !uplinkTypes[env.Type] {
			skipped++
			continue
		}
		if _, seen := appliedByOrigin[env.OriginClusterID][env.OriginIndex]; seen {
			deduped++
			continue
		}
		fresh = append(fresh, raftcluster.BridgeEnvelope{
			Type:            env.Type,
			OriginClusterID: env.OriginClusterID,
			OriginNodeID:    env.OriginNodeID,
			OriginIndex:     env.OriginIndex,
			Timestamp:       env.Timestamp,
			Payload:         env.Payload,
		})
	}
	if len(fresh) > 0 {
		// Back-pressure: only submit-bearing batches contend for a slot, and
		// duplicate-only batches already returned above without reaching here.
		// A full semaphore means the apply pipeline is saturated (FSM jammed):
		// return 503 immediately so the sender retries with its backoff instead
		// of piling another blocked handler onto the queue. No entries applied,
		// so 503 is safe — nothing is marked applied.
		select {
		case r.sem <- struct{}{}:
			defer func() { <-r.sem }()
		default:
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "replicate busy; retry"})
			return
		}

		// The wrapper carries the SENDER's cluster id so a 'both'-mode peer
		// never ships it back (loop prevention sees a foreign origin).
		cmd := raftcluster.Command{
			Type:            raftcluster.CmdBridgeEnvelopeBatch,
			OriginClusterID: sender,
			Timestamp:       time.Now().UTC(),
		}
		payload, err := json.Marshal(raftcluster.BridgeEnvelopeBatchPayload{Entries: fresh})
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "encode batch: " + err.Error()})
			return
		}
		cmd.Payload = payload
		// All fresh entries apply atomically as ONE raft entry: either the
		// whole submit lands or none does. Use a SHORT apply enqueue timeout: if
		// the FSM is busy the enqueue times out fast and we return a RETRYABLE
		// 503 (none recorded as applied) rather than blocking tens of seconds
		// and amplifying the sender's retry storm. Only fresh entries reach
		// here, never duplicates, so re-delivery is harmless.
		if _, err := r.svc.SubmitCommand(ctx, cmd, replicateApplyTimeout); err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "submit: " + err.Error()})
			return
		}
		// Record the whole batch in one INSERT ... ON CONFLICT DO NOTHING.
		// The entries DID apply; failing to record them means a redelivery
		// re-applies the same work forever, so surface it as a 500 to retry.
		marks := make([]AppliedRemoteLog, 0, len(fresh))
		for _, env := range fresh {
			marks = append(marks, AppliedRemoteLog{
				OriginClusterID: env.OriginClusterID,
				OriginNodeID:    env.OriginNodeID,
				OriginIndex:     env.OriginIndex,
			})
		}
		if err := MarkAppliedBatch(ctx, r.db, marks); err != nil {
			if r.logger != nil {
				r.logger.Warn("bridge: MarkApplied failed", "error", err)
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "mark applied: " + err.Error()})
			return
		}
	}
	r.maybePrune(ctx)
	c.JSON(http.StatusOK, gin.H{"applied": len(fresh), "deduped": deduped, "skipped": skipped})
}

// maybePrune trims applied_remote_log at most once an hour. The table exists
// to dedupe sender RETRIES (a window of seconds) — without GC a hub absorbing
// one metric batch per host every 5s would grow it by ~17k rows/host/day
// forever. 48h of history is orders of magnitude more than retries need.
func (r *Receiver) maybePrune(ctx context.Context) {
	now := time.Now().Unix()
	last := atomic.LoadInt64(&r.lastPruneUnix)
	if now-last < 3600 || !atomic.CompareAndSwapInt64(&r.lastPruneUnix, last, now) {
		return
	}
	cutoff := time.Now().Add(-48 * time.Hour)
	if err := r.db.WithContext(ctx).
		Where("applied_at < ?", cutoff).
		Delete(&AppliedRemoteLog{}).Error; err != nil && r.logger != nil {
		r.logger.Warn("bridge: applied_remote_log prune failed", "error", err)
	}
}
