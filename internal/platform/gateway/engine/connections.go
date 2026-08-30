package engine

// Connection tracking for the gateway: tails Traefik's JSON access log on the
// gateway node and keeps ALL statistics in bounded RAM — nothing is written to
// the database or disk (the log file itself is truncated at a size cap after
// being read). A restart starts the stats over; the UI shows "since <t>".
//
// Memory budget (hard caps, worst case well under 1 MB):
//   - ring of the last ringSize requests (compact, strings clipped)
//   - per-client-IP counters, at most maxIPs entries with half-eviction
//   - fixed minute/hour buckets for the rate charts

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/log"
)

const (
	ringSize      = 600
	maxIPs        = 4096
	minuteBuckets = 120 // 2h of per-minute rate
	hourBuckets   = 48  // 48h of per-hour rate
	// maxLogBytes: the tracker truncates the access log once it has read past
	// this size. Traefik writes with O_APPEND, so a truncate is safe.
	maxLogBytes = 16 << 20
	pollEvery   = 2 * time.Second
)

// scannerNeedles are lower-case path fragments that practically only vulnerability
// scanners request. One hit marks the client suspicious.
var scannerNeedles = []string{
	"/.env", "/.git", "/.aws", "/wp-login", "/wp-admin", "/wp-content", "/xmlrpc.php",
	"/phpmyadmin", "/phpunit", "/cgi-bin/", "/actuator", "/owa/", "/autodiscover",
	"/etc/passwd", "/boaform", "/hnap1", "/manager/html", "/solr/", "/druid/",
	"/config.json", "/telescope", "/_ignition", "/geoserver", "/webui/",
}

// ConnEvent is one request in the live feed.
type ConnEvent struct {
	TS      int64  `json:"ts"` // unix seconds
	IP      string `json:"ip"`
	Host    string `json:"host"`
	Method  string `json:"method"`
	Path    string `json:"path"`
	Status  int    `json:"status"`
	RouteID string `json:"route_id,omitempty"` // ns-<id> router → <id>; "" = no route matched
	Blocked bool   `json:"blocked,omitempty"`
	DurMs   int32  `json:"dur_ms"`
	UA      string `json:"ua,omitempty"`
	TLS     bool   `json:"tls,omitempty"`
}

type ipStat struct {
	Count       uint32
	S2xx        uint32
	S3xx        uint32
	S4xx        uint32
	S5xx        uint32
	NoRoute     uint32
	ScannerHits uint32
	Blocked     uint32
	FirstSeen   int64
	LastSeen    int64
	LastPath    string
	LastHost    string
	LastUA      string
	// hosts tracks up to a handful of distinct request hosts (scanner tell).
	hosts [4]string
	nhost uint8
}

func (s *ipStat) noteHost(h string) {
	if h == "" {
		return
	}
	for i := uint8(0); i < s.nhost && i < 4; i++ {
		if s.hosts[i] == h {
			return
		}
	}
	if s.nhost < 4 {
		s.hosts[s.nhost] = h
	}
	s.nhost++ // saturating count of distinct hosts (>4 just means "many")
}

type rateBucket struct {
	Key     int64 // unix minute/hour index the bucket currently holds
	Total   uint32
	E4xx    uint32
	E5xx    uint32
	Blocked uint32
}

// ConnTracker tails the access log and aggregates. Nil-safe: a nil tracker
// answers "unavailable".
type ConnTracker struct {
	logger *log.Logger

	mu        sync.Mutex
	path      string
	ownFile   bool // we may truncate (managed mode: the log exists for us)
	cancel    context.CancelFunc
	startedAt time.Time

	ring    [ringSize]ConnEvent
	ringN   int // total ever written (position = ringN % ringSize)
	ips     map[string]*ipStat
	minutes []rateBucket // minuteBuckets entries, indexed by unix-minute % len
	hours   []rateBucket // hourBuckets entries, indexed by unix-hour % len

	total   uint64
	noRoute uint64
	blocked uint64

	offset int64
	lastFI os.FileInfo // identity of the file last read (rotation detection)
}

// NewConnTracker creates an idle tracker; Ensure() starts/retargets it.
func NewConnTracker(logger *log.Logger) *ConnTracker {
	return &ConnTracker{
		logger:  logger,
		ips:     map[string]*ipStat{},
		minutes: make([]rateBucket, minuteBuckets),
		hours:   make([]rateBucket, hourBuckets),
	}
}

// Ensure makes the tracker tail path (starting or retargeting as needed).
// ownFile allows size-cap truncation (only for the managed log we provision).
func (t *ConnTracker) Ensure(ctx context.Context, path string, ownFile bool) {
	if t == nil || path == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.cancel != nil && t.path == path {
		return
	}
	if t.cancel != nil {
		t.cancel()
	}
	rc, cancel := context.WithCancel(ctx)
	t.cancel = cancel
	t.path = path
	t.ownFile = ownFile
	t.offset = 0
	t.lastFI = nil
	t.startedAt = time.Now().UTC()
	// Skip history on first attach: stats measure "from now".
	if fi, err := os.Stat(path); err == nil {
		t.offset = fi.Size()
		t.lastFI = fi
	}
	go t.run(rc, path)
}

// Stop halts tailing (gateway moved away / disabled). Keeps collected data.
func (t *ConnTracker) Stop() {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.cancel != nil {
		t.cancel()
		t.cancel = nil
		t.path = ""
	}
}

// Running reports whether a tail loop is active.
func (t *ConnTracker) Running() bool {
	if t == nil {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.cancel != nil
}

func (t *ConnTracker) run(ctx context.Context, path string) {
	tick := time.NewTicker(pollEvery)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		t.mu.Lock()
		if t.path != path { // retargeted
			t.mu.Unlock()
			return
		}
		offset, lastFI, own := t.offset, t.lastFI, t.ownFile
		t.mu.Unlock()

		fi, err := os.Stat(path)
		if err != nil {
			continue
		}
		if lastFI == nil || !os.SameFile(lastFI, fi) || fi.Size() < offset {
			offset = 0 // rotated / truncated / first open
		}
		if fi.Size() == offset {
			t.maybeTruncate(path, offset, own)
			continue
		}
		f, err := os.Open(path) // #nosec G304 — our own data-dir path
		if err != nil {
			continue
		}
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			_ = f.Close()
			continue
		}
		r := bufio.NewReaderSize(f, 64<<10)
		read := offset
		for {
			line, err := r.ReadBytes('\n')
			if err != nil { // EOF mid-line: retry the partial line next tick
				break
			}
			read += int64(len(line))
			t.ingestLine(line)
		}
		_ = f.Close()
		t.mu.Lock()
		t.offset, t.lastFI = read, fi
		t.mu.Unlock()
		t.maybeTruncate(path, read, own)
	}
}

// maybeTruncate caps the on-disk log once fully read. Traefik keeps the fd
// open with O_APPEND, so writes continue safely at the new (zero) offset.
func (t *ConnTracker) maybeTruncate(path string, offset int64, own bool) {
	if !own || offset < maxLogBytes {
		return
	}
	if err := os.Truncate(path, 0); err == nil {
		t.mu.Lock()
		t.offset = 0
		t.mu.Unlock()
		if t.logger != nil {
			t.logger.Info("gateway: truncated access log at size cap", "path", path, "bytes", offset)
		}
	}
}

// accessLine is the subset of Traefik's JSON access-log entry we read.
type accessLine struct {
	ClientHost       string `json:"ClientHost"`
	ClientAddr       string `json:"ClientAddr"`
	RequestHost      string `json:"RequestHost"`
	RequestMethod    string `json:"RequestMethod"`
	RequestPath      string `json:"RequestPath"`
	RequestScheme    string `json:"RequestScheme"`
	DownstreamStatus int    `json:"DownstreamStatus"`
	RouterName       string `json:"RouterName"`
	Duration         int64  `json:"Duration"` // ns
	EntryPointName   string `json:"entryPointName"`
	StartUTC         string `json:"StartUTC"`
	UserAgent        string `json:"request_User-Agent"`
}

func (t *ConnTracker) ingestLine(line []byte) {
	line = trimSpaceBytes(line)
	if len(line) == 0 || line[0] != '{' {
		return
	}
	var a accessLine
	if json.Unmarshal(line, &a) != nil {
		return
	}
	if a.EntryPointName == "ping" || a.EntryPointName == "traefik" {
		return
	}
	ip := a.ClientHost
	if ip == "" {
		ip = hostOnly(a.ClientAddr)
	}
	if ip == "" {
		return
	}
	ts := time.Now().UTC()
	if a.StartUTC != "" {
		if p, err := time.Parse(time.RFC3339Nano, a.StartUTC); err == nil {
			ts = p.UTC()
		}
	}
	routeID, blocked := routerRouteID(a.RouterName)
	ev := ConnEvent{
		TS:      ts.Unix(),
		IP:      ip,
		Host:    clip(strings.ToLower(a.RequestHost), 80),
		Method:  a.RequestMethod,
		Path:    clip(a.RequestPath, 96),
		Status:  a.DownstreamStatus,
		RouteID: routeID,
		Blocked: blocked,
		DurMs:   int32(a.Duration / int64(time.Millisecond)),
		UA:      clip(a.UserAgent, 72),
		TLS:     a.RequestScheme == "https" || a.EntryPointName == "websecure",
	}
	scanner := isScannerPath(a.RequestPath)

	t.mu.Lock()
	defer t.mu.Unlock()

	t.ring[t.ringN%ringSize] = ev
	t.ringN++

	t.total++
	noRoute := routeID == "" && !blocked
	if noRoute {
		t.noRoute++
	}
	if blocked {
		t.blocked++
	}

	s := t.ips[ip]
	if s == nil {
		if len(t.ips) >= maxIPs {
			t.evictIPsLocked()
		}
		s = &ipStat{FirstSeen: ev.TS}
		t.ips[ip] = s
	}
	s.Count++
	switch {
	case ev.Status >= 500:
		s.S5xx++
	case ev.Status >= 400:
		s.S4xx++
	case ev.Status >= 300:
		s.S3xx++
	case ev.Status >= 200:
		s.S2xx++
	}
	if noRoute {
		s.NoRoute++
	}
	if blocked {
		s.Blocked++
	}
	if scanner {
		s.ScannerHits++
	}
	s.LastSeen = ev.TS
	s.LastPath = ev.Path
	s.LastHost = ev.Host
	if ev.UA != "" {
		s.LastUA = ev.UA
	}
	s.noteHost(ev.Host)

	bump(t.minutes, ev.TS/60, ev.Status, blocked)
	bump(t.hours, ev.TS/3600, ev.Status, blocked)
}

// evictIPsLocked drops the ~half of tracked IPs least worth keeping (lowest
// request count, oldest last-seen) so the map stays bounded (perf rule: never
// let a churning-id map grow unchecked).
func (t *ConnTracker) evictIPsLocked() {
	type kv struct {
		ip   string
		rank uint64
	}
	all := make([]kv, 0, len(t.ips))
	for ip, s := range t.ips {
		// rank: recency dominates, count breaks ties.
		c := s.Count
		if c > 0xffff {
			c = 0xffff
		}
		all = append(all, kv{ip, uint64(s.LastSeen)<<16 | uint64(c)})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].rank < all[j].rank })
	for _, e := range all[:len(all)/2] {
		delete(t.ips, e.ip)
	}
}

func bump(s []rateBucket, key int64, status int, blocked bool) {
	b := &s[key%int64(len(s))]
	if b.Key != key {
		*b = rateBucket{Key: key}
	}
	b.Total++
	if status >= 500 {
		b.E5xx++
	} else if status >= 400 {
		b.E4xx++
	}
	if blocked {
		b.Blocked++
	}
}

// --- read side ---------------------------------------------------------------

// ConnIP is one client in the "top clients" table.
type ConnIP struct {
	IP          string `json:"ip"`
	Count       uint32 `json:"count"`
	S2xx        uint32 `json:"s2xx"`
	S4xx        uint32 `json:"s4xx"`
	S5xx        uint32 `json:"s5xx"`
	NoRoute     uint32 `json:"no_route"`
	ScannerHits uint32 `json:"scanner_hits"`
	Blocked     uint32 `json:"blocked"`
	FirstSeen   int64  `json:"first_seen"`
	LastSeen    int64  `json:"last_seen"`
	LastPath    string `json:"last_path,omitempty"`
	LastHost    string `json:"last_host,omitempty"`
	LastUA      string `json:"last_ua,omitempty"`
	Hosts       int    `json:"hosts"`
	// Suspicion 0–100 from simple heuristics (scanner paths, error ratio,
	// no-route hits, host spraying).
	Suspicion int `json:"suspicion"`
	// IsBlocked: an active gateway_blocks entry already covers this IP.
	IsBlocked bool `json:"is_blocked,omitempty"`
}

// RatePoint is one chart bucket.
type RatePoint struct {
	TS      int64  `json:"ts"` // bucket start, unix seconds
	Total   uint32 `json:"total"`
	E4xx    uint32 `json:"e4xx"`
	E5xx    uint32 `json:"e5xx"`
	Blocked uint32 `json:"blocked"`
}

// ConnectionsView is the GET /gateway/connections payload.
type ConnectionsView struct {
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
	SinceTS   int64  `json:"since_ts,omitempty"`
	Total     uint64 `json:"total"`
	NoRoute   uint64 `json:"no_route"`
	BlockedN  uint64 `json:"blocked_total"`
	UniqueIPs int    `json:"unique_ips"`
	// Minutes: last 60 minute-buckets (oldest first); Hours: last 48.
	Minutes []RatePoint `json:"minutes"`
	Hours   []RatePoint `json:"hours"`
	Top     []ConnIP    `json:"top"`
	Recent  []ConnEvent `json:"recent"`
}

// Snapshot renders the current stats (topN clients, recentN feed rows).
func (t *ConnTracker) Snapshot(topN, recentN int) *ConnectionsView {
	if t == nil {
		return &ConnectionsView{Available: false, Reason: "connection tracking is not running on this node"}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.startedAt.IsZero() {
		return &ConnectionsView{Available: false, Reason: "connection tracking has not started yet"}
	}
	v := &ConnectionsView{
		Available: true,
		SinceTS:   t.startedAt.Unix(),
		Total:     t.total,
		NoRoute:   t.noRoute,
		BlockedN:  t.blocked,
		UniqueIPs: len(t.ips),
	}
	now := time.Now().UTC()
	v.Minutes = series(t.minutes, now.Unix()/60, 60)
	v.Hours = series(t.hours, now.Unix()/3600, 48)

	// Top clients.
	ips := make([]ConnIP, 0, len(t.ips))
	for ip, s := range t.ips {
		ips = append(ips, ConnIP{
			IP: ip, Count: s.Count, S2xx: s.S2xx, S4xx: s.S4xx, S5xx: s.S5xx,
			NoRoute: s.NoRoute, ScannerHits: s.ScannerHits, Blocked: s.Blocked,
			FirstSeen: s.FirstSeen, LastSeen: s.LastSeen,
			LastPath: s.LastPath, LastHost: s.LastHost, LastUA: s.LastUA,
			Hosts: int(s.nhost), Suspicion: suspicion(s),
		})
	}
	sort.Slice(ips, func(i, j int) bool {
		if ips[i].Suspicion != ips[j].Suspicion {
			return ips[i].Suspicion > ips[j].Suspicion
		}
		return ips[i].Count > ips[j].Count
	})
	if topN <= 0 {
		topN = 50
	}
	if len(ips) > topN {
		ips = ips[:topN]
	}
	v.Top = ips

	// Recent feed, newest first.
	if recentN <= 0 {
		recentN = 100
	}
	n := t.ringN
	if n > ringSize {
		n = ringSize
	}
	if recentN > n {
		recentN = n
	}
	v.Recent = make([]ConnEvent, 0, recentN)
	for i := 0; i < recentN; i++ {
		v.Recent = append(v.Recent, t.ring[((t.ringN-1-i)%ringSize+ringSize)%ringSize])
	}
	return v
}

func series(arr []rateBucket, nowKey int64, n int) []RatePoint {
	out := make([]RatePoint, 0, n)
	for k := nowKey - int64(n) + 1; k <= nowKey; k++ {
		b := arr[k%int64(len(arr))]
		p := RatePoint{TS: k}
		if b.Key == k {
			p.Total, p.E4xx, p.E5xx, p.Blocked = b.Total, b.E4xx, b.E5xx, b.Blocked
		}
		out = append(out, p)
	}
	return out
}

func suspicion(s *ipStat) int {
	score := 0
	if s.ScannerHits > 0 {
		score += 40
	}
	errs := s.S4xx + s.S5xx
	if s.Count >= 10 && errs*10 >= s.Count*6 { // >60% errors
		score += 30
	}
	if s.NoRoute >= 5 {
		score += 20
	}
	if s.nhost > 3 {
		score += 10
	}
	if score > 100 {
		score = 100
	}
	return score
}

// --- helpers -------------------------------------------------------------------

func isScannerPath(p string) bool {
	lp := strings.ToLower(p)
	for _, n := range scannerNeedles {
		if strings.Contains(lp, n) {
			return true
		}
	}
	return false
}

// routerRouteID extracts the node-stats route id from a Traefik router name
// ("ns-ab12cd@file" → "ab12cd"); the blocklist routers report blocked=true.
func routerRouteID(router string) (id string, blocked bool) {
	r := router
	if i := strings.IndexByte(r, '@'); i >= 0 {
		r = r[:i]
	}
	if strings.HasPrefix(r, "ns-blocklist") {
		return "", true
	}
	if strings.HasPrefix(r, "ns-") {
		return strings.TrimSuffix(strings.TrimPrefix(r, "ns-"), "-http"), false
	}
	return "", false
}

func hostOnly(addr string) string {
	if i := strings.LastIndexByte(addr, ':'); i > 0 && !strings.Contains(addr[i:], "]") {
		return strings.Trim(addr[:i], "[]")
	}
	return strings.Trim(addr, "[]")
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func trimSpaceBytes(b []byte) []byte {
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == '\r' || b[len(b)-1] == ' ') {
		b = b[:len(b)-1]
	}
	for len(b) > 0 && b[0] == ' ' {
		b = b[1:]
	}
	return b
}
