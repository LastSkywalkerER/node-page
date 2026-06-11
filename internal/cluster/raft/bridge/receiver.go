package bridge

import (
	"context"
	"encoding/json"
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
	if sender == "" || sender == r.myCluster {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid sender cluster id"})
		return
	}

	var batch Batch
	if err := json.Unmarshal(body, &batch); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "decode batch: " + err.Error()})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()

	var applied, deduped, skipped int
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
		cmd := raftcluster.Command{
			Type:            env.Type,
			OriginClusterID: env.OriginClusterID,
			OriginNodeID:    env.OriginNodeID,
			OriginIndex:     env.OriginIndex,
			Timestamp:       env.Timestamp,
			Payload:         env.Payload,
		}
		if _, err := r.svc.SubmitCommand(ctx, cmd, 5*time.Second); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "submit: " + err.Error()})
			return
		}
		if err := MarkApplied(ctx, r.db, env.OriginClusterID, env.OriginNodeID, env.OriginIndex); err != nil {
			if r.logger != nil {
				r.logger.Warn("bridge: MarkApplied failed", "error", err)
			}
		}
		applied++
	}
	r.maybePrune(ctx)
	c.JSON(http.StatusOK, gin.H{"applied": applied, "deduped": deduped, "skipped": skipped})
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
