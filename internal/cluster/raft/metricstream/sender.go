// Package metricstream implements the best-effort, non-durable metric
// replication path between nodes. Metrics are high-volume, low-value,
// drop-tolerant time-series; pushing them through the fsync'd Raft consensus log
// is what ballooned the on-disk log (and its RSS). Instead, each node POSTs its
// own host's metric batch directly to its peers (intra-cluster P2P) and, in
// push/both bridge mode, to the cross-cluster hub seeds. Delivery is best-effort:
// a dropped batch leaves at most a one-tick gap that the next full-state resend
// heals. Host SPECS + identity still flow through Raft (CmdHostUpsert); this
// path only ATTACHES metrics to an already-known host.
package metricstream

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/charmbracelet/log"
	"gorm.io/gorm"

	"system-stats/internal/app/config"
	raftcluster "system-stats/internal/cluster/raft"
	"system-stats/internal/cluster/raft/bridge"
)

// RoutePath is the receiver route RELATIVE to the /api/v1 router group (used for
// registration); Path is the full path the sender POSTs to (public, HMAC-auth).
const (
	RoutePath = "/cluster/metrics"
	Path      = "/api/v1" + RoutePath
)

const shipTimeout = 4 * time.Second

// Sender ships THIS node's collected metric batch to its peers. It holds no
// state beyond config; Broadcast is called once per collection tick.
type Sender struct {
	logger     *log.Logger
	db         *gorm.DB
	httpClient *http.Client

	localCluster string
	localNode    string
	intraSecret  string // cluster-shared HMAC key for same-cluster peers (JWT secret)

	// Cross-cluster uplink (optional): in push/both bridge mode this node also
	// ships its metrics to the hub seeds, signed with the bridge shared secret.
	// Guarded by mu — SetBridge updates them live when the uplink is
	// (re)configured at runtime, so metrics follow the same target as the
	// topology bridge without a process restart.
	mu           sync.RWMutex
	bridgeSecret string
	bridgeMode   string
	hubSeeds     []string

	// gzipDisabled is set once a peer rejects a gzipped body (an older node that
	// can't decompress returns 400). The newer side degrades to plain bodies so a
	// version skew doesn't black-hole metrics. Reset on restart.
	gzipDisabled atomic.Bool
}

// NewSender builds a metric-stream sender. intraSecret is the cluster-shared key
// (JWT secret) used to sign same-cluster posts; bridge carries the optional
// cross-cluster uplink config.
func NewSender(logger *log.Logger, db *gorm.DB, localCluster, localNode, intraSecret string, b config.RaftBridgeConfig) *Sender {
	return &Sender{
		logger:       logger,
		db:           db,
		httpClient:   &http.Client{Timeout: shipTimeout},
		localCluster: localCluster,
		localNode:    localNode,
		intraSecret:  intraSecret,
		bridgeSecret: b.SharedSecret,
		bridgeMode:   config.NormalizeBridgeMode(b.Mode),
		hubSeeds:     b.RemoteSeeds,
	}
}

// SetBridge updates the cross-cluster uplink target live (mode / shared secret /
// hub seeds). Called whenever the bridge is (re)configured at runtime so the
// metric stream self-heals onto the new hub without a restart — otherwise the
// topology bridge would follow a live "Apply" while metrics kept shipping to the
// seeds captured at Raft activation (often empty on a freshly (re)created
// cluster), which silently black-holes the hub's metrics.
func (s *Sender) SetBridge(b config.RaftBridgeConfig) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.bridgeSecret = b.SharedSecret
	s.bridgeMode = config.NormalizeBridgeMode(b.Mode)
	s.hubSeeds = append([]string(nil), b.RemoteSeeds...)
}

// Broadcast ships payload (this node's host metrics) to all intra-cluster peers
// and, when uplinking, to the cross-cluster hub. Fire-and-forget per target: a
// slow/unreachable peer never blocks the collection cycle and a failed POST is
// silently dropped (the next tick re-sends the full current state).
func (s *Sender) Broadcast(ctx context.Context, payload raftcluster.MetricBatchPayload) {
	body, err := json.Marshal(payload)
	if err != nil {
		return
	}
	// gzip once for all targets — the docker-heavy JSON compresses ~5-10x.
	// Skip if a peer previously rejected a gzipped body (older, pre-gzip node).
	encoding := ""
	if !s.gzipDisabled.Load() {
		if gz, ok := bridge.Gzip(body); ok {
			body, encoding = gz, bridge.EncodingGzip
		}
	}

	// Intra-cluster peers (P2P): discovered from the replicated advertise catalog.
	if s.intraSecret != "" {
		peers, err := raftcluster.ListClusterPeerURLs(ctx, s.db, s.localCluster, s.localNode)
		if err != nil {
			if s.logger != nil {
				s.logger.Debug("metricstream: list peers failed", "error", err)
			}
		}
		for _, u := range peers {
			s.fire(u, body, encoding, s.intraSecret)
		}
	}

	// Cross-cluster hub uplink (push/both): same payload, bridge-secret signed.
	// Snapshot under the lock — SetBridge may swap these out concurrently.
	s.mu.RLock()
	secret, mode, seeds := s.bridgeSecret, s.bridgeMode, s.hubSeeds
	s.mu.RUnlock()
	if secret != "" && (mode == config.BridgeModePush || mode == config.BridgeModeBoth) {
		for _, u := range seeds {
			s.fire(u, body, encoding, secret)
		}
	}
}

func (s *Sender) fire(baseURL string, body []byte, encoding, secret string) {
	go func() {
		fireCtx, cancel := context.WithTimeout(context.Background(), shipTimeout)
		defer cancel()
		ts := time.Now().UnixNano()
		sig := bridge.Sign(secret, ts, body)
		url := strings.TrimRight(baseURL, "/") + Path
		req, err := http.NewRequestWithContext(fireCtx, http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			return
		}
		req.Header.Set("Content-Type", "application/json")
		if encoding != "" {
			req.Header.Set(bridge.EncodingHeader, encoding)
		}
		req.Header.Set(bridge.TimestampHeader, strconv.FormatInt(ts, 10))
		req.Header.Set(bridge.HMACHeader, sig)
		req.Header.Set(bridge.SenderClusterHeader, s.localCluster)
		req.Header.Set(bridge.SenderNodeHeader, s.localNode)
		resp, err := s.httpClient.Do(req)
		if err != nil {
			return // best-effort: drop on failure, next tick re-sends
		}
		// An older peer that can't decompress returns 400 on a gzipped body —
		// permanently fall back to plain bodies so a version skew doesn't
		// black-hole metrics (resumes after restart once peers are upgraded).
		if encoding == bridge.EncodingGzip && resp.StatusCode == http.StatusBadRequest {
			if s.gzipDisabled.CompareAndSwap(false, true) && s.logger != nil {
				s.logger.Warn("metricstream: peer rejected gzip; falling back to uncompressed bodies")
			}
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()
}
