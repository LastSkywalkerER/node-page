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

	lastPruneUnix int64 // atomic; applied_remote_log GC bookkeeping
}

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
	var deduped, skipped int
	fresh := make([]raftcluster.BridgeEnvelope, 0, len(batch.Entries))
	for _, env := range batch.Entries {
		if r.uplinkOnly && !uplinkTypes[env.Type] {
			skipped++
			continue
		}
		seen, err := HasApplied(ctx, r.db, env.OriginClusterID, env.OriginIndex)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "dedupe check: " + err.Error()})
			return
		}
		if seen {
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
		if _, err := r.svc.SubmitCommand(ctx, cmd, 30*time.Second); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "submit: " + err.Error()})
			return
		}
		for _, env := range fresh {
			if err := MarkApplied(ctx, r.db, env.OriginClusterID, env.OriginNodeID, env.OriginIndex); err != nil {
				if r.logger != nil {
					r.logger.Warn("bridge: MarkApplied failed", "error", err)
				}
			}
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
