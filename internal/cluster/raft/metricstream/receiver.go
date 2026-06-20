package metricstream

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/charmbracelet/log"
	"github.com/gin-gonic/gin"

	"system-stats/internal/app/config"
	raftcluster "system-stats/internal/cluster/raft"
	"system-stats/internal/cluster/raft/bridge"
)

const ingestTimeout = 10 * time.Second

// Ingester writes one decoded metric batch into local state + SSE. *MetricSink
// (in the raft package) is the production implementation; the interface keeps
// the receiver testable without a full repository stack.
type Ingester interface {
	Ingest(ctx context.Context, p raftcluster.MetricBatchPayload, origin string) error
}

// Receiver is the HTTP handler for POST /api/v1/cluster/metrics. It verifies the
// HMAC, decodes the metric batch, and writes it DIRECTLY into local state + SSE
// via the Ingester — no Raft, no consensus, no durable log entry.
type Receiver struct {
	logger       *log.Logger
	ingest       Ingester
	localCluster string

	// Both HMAC keys are mu-guarded so they can be swapped live. intraSecret (the
	// cluster-shared JWT key) starts as the boot env secret, which on a joined node
	// is a placeholder until BootstrapClusterSecrets discovers the real one —
	// SetIntraSecret swaps it in so same-cluster peers stop being rejected.
	// bridgeSecret is the cross-cluster HMAC key ("" if not configured);
	// SetBridgeSecret updates it when the uplink is (re)configured at runtime.
	mu           sync.RWMutex
	intraSecret  string // same-cluster HMAC key (JWT secret)
	bridgeSecret string
}

// NewReceiver wires the receiver. intraSecret authenticates same-cluster peers;
// bridge.SharedSecret (if set) authenticates cross-cluster uplinks.
func NewReceiver(logger *log.Logger, ingest Ingester, localCluster, intraSecret string, b config.RaftBridgeConfig) *Receiver {
	return &Receiver{
		logger:       logger,
		ingest:       ingest,
		localCluster: localCluster,
		intraSecret:  intraSecret,
		bridgeSecret: b.SharedSecret,
	}
}

// SetBridgeSecret updates the cross-cluster HMAC key live so a hub keeps
// authenticating uplinks after the shared secret is (re)configured at runtime.
func (r *Receiver) SetBridgeSecret(secret string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.bridgeSecret = secret
}

// SetIntraSecret swaps the cluster-shared HMAC key used to authenticate
// same-cluster peers. Called once the real cluster secret is known so a joined
// node stops rejecting peers that sign with the cluster key. No-op for empty.
func (r *Receiver) SetIntraSecret(secret string) {
	if secret == "" {
		return
	}
	r.mu.Lock()
	r.intraSecret = secret
	r.mu.Unlock()
}

// Handle processes one metric-stream POST. Mount under POST Path.
func (r *Receiver) Handle(c *gin.Context) {
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "read body: " + err.Error()})
		return
	}
	tsNanos, err := strconv.ParseInt(c.GetHeader(bridge.TimestampHeader), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid timestamp header"})
		return
	}
	sender := c.GetHeader(bridge.SenderClusterHeader)
	if sender == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing sender cluster id header"})
		return
	}

	// Pick the verification key by trust domain: same-cluster peers use the
	// cluster-shared JWT secret; a foreign cluster (cross-cluster uplink) uses
	// the bridge shared secret. A foreign sender with no bridge secret configured
	// here is rejected.
	r.mu.RLock()
	secret := r.intraSecret
	bridgeSecret := r.bridgeSecret
	r.mu.RUnlock()
	if sender != r.localCluster {
		if bridgeSecret == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "cross-cluster metric stream not enabled"})
			return
		}
		secret = bridgeSecret
	}
	if err := bridge.Verify(secret, c.GetHeader(bridge.HMACHeader), tsNanos, body); err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}

	// Decompress AFTER verifying the HMAC over the on-wire (compressed) bytes.
	body, err = bridge.Gunzip(c.GetHeader(bridge.EncodingHeader), body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "decompress: " + err.Error()})
		return
	}

	var p raftcluster.MetricBatchPayload
	if err := json.Unmarshal(body, &p); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "decode batch: " + err.Error()})
		return
	}

	// Detached from the request context: a client hang-up mid-write must not
	// abort the half-done DB write. origin "" for same-cluster.
	origin := sender
	if sender == r.localCluster {
		origin = ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), ingestTimeout)
	defer cancel()
	if err := r.ingest.Ingest(ctx, p, origin); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "ingest: " + err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}
