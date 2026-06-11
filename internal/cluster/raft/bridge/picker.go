package bridge

import (
	"context"
	"net/http"
	"strings"
	"sort"
	"sync"
	"time"

	"github.com/charmbracelet/log"
	"gorm.io/gorm"

	raftcluster "system-stats/internal/cluster/raft"
)

// Picker polls every URL advertised by the peer cluster's nodes, records
// per-URL median round-trip times and exposes Pick() for the bridge sender
// to choose the lowest-latency healthy URL.
//
// Probing uses GET /api/v1/raft/ping which is intentionally public so the
// picker doesn't need credentials. Sensitive mutating endpoints stay
// HMAC-authenticated.
type Picker struct {
	logger     *log.Logger
	db         *gorm.DB
	myCluster  string
	httpClient *http.Client
	probeEvery time.Duration
	// seeds are operator-provided peer URLs (RAFT_BRIDGE_REMOTE_SEEDS / the
	// uplink form). They bootstrap the bridge: the replicated catalog cannot
	// contain a peer cluster's URL before the bridge is up — chicken & egg —
	// so seeds are probed alongside catalog entries.
	seeds []string

	mu       sync.RWMutex
	measures map[string]*measure // keyed by URL
}

type measure struct {
	URL        string
	ClusterID  string
	NodeID     string
	RTT        time.Duration
	LastOK     time.Time
	LastErr    string
	Consecutive int // consecutive successes
}

// NewPicker wires the picker. myClusterID is used to filter the catalog
// (we only probe peer-cluster URLs, never our own).
func NewPicker(logger *log.Logger, db *gorm.DB, myClusterID string, seeds []string) *Picker {
	clean := make([]string, 0, len(seeds))
	for _, s := range seeds {
		if v := strings.TrimSuffix(strings.TrimSpace(s), "/"); v != "" {
			clean = append(clean, v)
		}
	}
	return &Picker{
		logger:     logger,
		db:         db,
		myCluster:  myClusterID,
		httpClient: &http.Client{Timeout: 3 * time.Second},
		probeEvery: 30 * time.Second,
		seeds:      clean,
		measures:   make(map[string]*measure),
	}
}

// Run polls the catalog at probeEvery cadence until ctx is cancelled.
func (p *Picker) Run(ctx context.Context) {
	tick := time.NewTicker(p.probeEvery)
	defer tick.Stop()
	p.probeOnce(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			p.probeOnce(ctx)
		}
	}
}

func (p *Picker) probeOnce(ctx context.Context) {
	entries, err := raftcluster.ListPeerAdvertises(ctx, p.db)
	if err != nil {
		if p.logger != nil {
			p.logger.Warn("bridge picker: list catalog failed", "error", err)
		}
		return
	}
	// Seeds first, then catalog rows; catalog entries carry richer identity
	// (cluster/node ids) and overwrite a bare seed measure for the same URL.
	seen := map[string]bool{}
	probeList := make([]raftcluster.PeerAdvertise, 0, len(p.seeds)+len(entries))
	for _, u := range p.seeds {
		probeList = append(probeList, raftcluster.PeerAdvertise{URL: u, ClusterID: "", NodeID: "seed"})
		seen[u] = true
	}
	for _, e := range entries {
		if e.ClusterID == p.myCluster || e.URL == "" || seen[e.URL] {
			continue
		}
		probeList = append(probeList, e)
	}
	var wg sync.WaitGroup
	for _, e := range probeList {
		entry := e
		wg.Add(1)
		go func() {
			defer wg.Done()
			p.probeURL(ctx, entry)
		}()
	}
	wg.Wait()
}

func (p *Picker) probeURL(ctx context.Context, e raftcluster.PeerAdvertise) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, e.URL+"/api/v1/raft/ping", nil)
	if err != nil {
		p.recordFailure(e, err.Error())
		return
	}
	start := time.Now()
	resp, err := p.httpClient.Do(req)
	if err != nil {
		p.recordFailure(e, err.Error())
		return
	}
	defer resp.Body.Close()
	rtt := time.Since(start)
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode/100 != 2 {
		p.recordFailure(e, "http "+resp.Status)
		return
	}
	p.recordSuccess(e, rtt)
}

func (p *Picker) recordSuccess(e raftcluster.PeerAdvertise, rtt time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()
	m, ok := p.measures[e.URL]
	if !ok {
		m = &measure{URL: e.URL, ClusterID: e.ClusterID, NodeID: e.NodeID}
		p.measures[e.URL] = m
	}
	// Exponential moving average to smooth jitter.
	if m.RTT == 0 {
		m.RTT = rtt
	} else {
		m.RTT = (m.RTT*7 + rtt) / 8
	}
	m.LastOK = time.Now()
	m.LastErr = ""
	m.Consecutive++
}

func (p *Picker) recordFailure(e raftcluster.PeerAdvertise, reason string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	m, ok := p.measures[e.URL]
	if !ok {
		m = &measure{URL: e.URL, ClusterID: e.ClusterID, NodeID: e.NodeID}
		p.measures[e.URL] = m
	}
	m.LastErr = reason
	m.Consecutive = 0
}

// Snapshot returns a sorted-by-RTT view of all currently-probed peer URLs.
// Used by the admin UI / status endpoint.
type Sample struct {
	URL         string        `json:"url"`
	ClusterID   string        `json:"cluster_id"`
	NodeID      string        `json:"node_id"`
	RTT         time.Duration `json:"rtt"`
	LastOK      time.Time     `json:"last_ok,omitempty"`
	LastErr     string        `json:"last_err,omitempty"`
	Consecutive int           `json:"consecutive_ok"`
	Healthy     bool          `json:"healthy"`
}

// Snapshot returns a stable, sorted list of current measurements.
func (p *Picker) Snapshot() []Sample {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]Sample, 0, len(p.measures))
	for _, m := range p.measures {
		out = append(out, Sample{
			URL:         m.URL,
			ClusterID:   m.ClusterID,
			NodeID:      m.NodeID,
			RTT:         m.RTT,
			LastOK:      m.LastOK,
			LastErr:     m.LastErr,
			Consecutive: m.Consecutive,
			Healthy:     m.LastErr == "" && m.Consecutive > 0,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		// Healthy before unhealthy; within each group, lowest RTT first.
		if out[i].Healthy != out[j].Healthy {
			return out[i].Healthy
		}
		return out[i].RTT < out[j].RTT
	})
	return out
}

// Pick returns the URL of the best healthy peer node, or "" when none is
// known. peerClusterID restricts the search to a specific peer cluster;
// pass "" to consider any cluster other than ours.
func (p *Picker) Pick(peerClusterID string) string {
	for _, s := range p.Snapshot() {
		if !s.Healthy {
			continue
		}
		if peerClusterID != "" && s.ClusterID != peerClusterID {
			continue
		}
		return s.URL
	}
	return ""
}
