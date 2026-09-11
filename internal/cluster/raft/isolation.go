package raft

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/log"
	hraft "github.com/hashicorp/raft"
	"gorm.io/gorm"

	"system-stats/internal/app/config"
	hosts "system-stats/internal/cluster/hosts"
	"system-stats/internal/platform/hostnet"
)

// The "deaf node" case this detector exists for: a node whose ADVERTISED Raft
// address no longer reaches it (the machine moved and got a new IP while its
// .env still pins the old one; a NAT/port-forward rule went away). Peers dial
// the dead address, so this node never hears a leader and sits in an endless
// pre-vote loop; it cannot commit or forward a single write, so its own host
// record (IP, boot time, kernel, …) is frozen cluster-wide. Meanwhile its
// OUTBOUND paths still work — it keeps POSTing metrics to every peer and the
// hub — so the machine card stays green with live gauges and nobody notices
// the record is stale or the node is unwritable. The detector recognises the
// asymmetry and publishes a hosts.NodeAlert over that very metric stream, and
// the sender throttles the stream to a liveness cadence while it lasts.
const (
	// isolationDeafAfter is how long this node must go without hearing a leader
	// before it starts asking peers whether the cluster has one. Well above the
	// membership manager's 90 s demote window and any election storm.
	isolationDeafAfter = 2 * time.Minute
	isolationTick      = 15 * time.Second
	isolationPingTO    = 2500 * time.Millisecond
)

// isolationCause explains WHY peers cannot reach this node.
type isolationCause int

const (
	causeNone isolationCause = iota
	// causeStaleAdvertise: the advertised Raft IP is not on this machine at all.
	causeStaleAdvertise
	// causeUnreachable: the address looks local, yet peers that DO have a
	// leader never reach us — firewall, NAT, wrong port forward.
	causeUnreachable
)

// isolationInputs is everything the pure decision needs, gathered per tick.
type isolationInputs struct {
	enabled     bool
	state       string // hashicorp/raft state string ("Leader", "Follower", …)
	leaderID    string
	noLeaderFor time.Duration

	advertiseIP   string // literal IP from RAFT_ADVERTISE_ADDR; "" = hostname/unknown
	localIPs      []string
	localIPsKnown bool

	peersAnswered   int // catalog peers that answered /raft/ping
	peersWithLeader int // …of which report a leader (or lead themselves)
}

// decideIsolation is the pure rule. It errs on the side of silence: with no
// peer answering it cannot tell "I am cut off" from "everything is down", and
// a leaderless cluster whose members can all be reached is the existing
// "cannot elect a leader" banner's business, not this one's.
func decideIsolation(in isolationInputs, deafAfter time.Duration) isolationCause {
	if !in.enabled || in.state == hraft.Leader.String() || in.leaderID != "" {
		return causeNone
	}
	if in.noLeaderFor < deafAfter || in.peersAnswered == 0 {
		return causeNone
	}
	if in.advertiseIP != "" && in.localIPsKnown && !containsIP(in.localIPs, in.advertiseIP) &&
		!looksLikeNAT(in.advertiseIP, in.localIPs) {
		return causeStaleAdvertise
	}
	if in.peersWithLeader > 0 {
		return causeUnreachable
	}
	return causeNone
}

// looksLikeNAT reports whether the advertised address is plausibly a correct
// PUBLIC address of a machine that only holds private ones — a node behind NAT
// or a cloud VM with a mapped address. Such an address is not on any local
// interface either, but calling it "stale" would push the operator to rewrite
// a setting that is right; the fault is in the path, not the value. Only the
// wording changes: the node is reported as unreachable instead.
func looksLikeNAT(advertiseIP string, localIPs []string) bool {
	adv := net.ParseIP(advertiseIP)
	if adv == nil || isPrivateIP(adv) {
		return false
	}
	for _, v := range localIPs {
		if ip := net.ParseIP(v); ip != nil && !isPrivateIP(ip) {
			return false // the machine does hold a public address — compare honestly
		}
	}
	return true
}

// isPrivateIP covers the address ranges a LAN machine actually gets: RFC1918 /
// CGNAT / link-local / loopback (and their IPv6 equivalents).
func isPrivateIP(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsPrivate() {
		return true
	}
	// 100.64.0.0/10 (CGNAT, and what Tailscale hands out) is not covered by
	// net.IP.IsPrivate but is never reachable from outside either.
	if v4 := ip.To4(); v4 != nil {
		return v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127
	}
	return false
}

func containsIP(list []string, ip string) bool {
	for _, v := range list {
		if v == ip {
			return true
		}
	}
	return false
}

// advertiseIPFromAddr extracts the literal IPv4/IPv6 host of a host:port
// advertise address; "" for an empty address, a bare port (":7000") or a DNS
// name (which the local-address check cannot judge).
func advertiseIPFromAddr(addr string) string {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return ""
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	if ip := net.ParseIP(host); ip != nil && !ip.IsUnspecified() {
		return ip.String()
	}
	return ""
}

// isolationFacts are the concrete values the alert text is built from.
type isolationFacts struct {
	nodeID        string
	advertiseAddr string
	advertiseURL  string
	localIPv4     string // where the machine actually is; "" unknown
	raftPort      string
	httpPort      string
	envFile       string
	noLeaderFor   time.Duration
}

// buildIsolationAlert renders the operator-facing alert for a cause.
func buildIsolationAlert(cause isolationCause, f isolationFacts, since time.Time) *hosts.NodeAlert {
	if cause == causeNone {
		return nil
	}
	a := &hosts.NodeAlert{
		Kind:          hosts.NodeAlertRaftIsolated,
		Severity:      hosts.NodeAlertSeverityError,
		NodeID:        f.nodeID,
		AdvertiseAddr: f.advertiseAddr,
		AdvertiseURL:  f.advertiseURL,
		LocalIPv4:     f.localIPv4,
		Since:         since,
	}
	oldIP := advertiseIPFromAddr(f.advertiseAddr)
	newIP := f.localIPv4
	if newIP == "" {
		newIP = "<this machine's IP>"
	}
	raftPort := f.raftPort
	if raftPort == "" {
		raftPort = "7000"
	}
	httpPort := f.httpPort
	if httpPort == "" {
		httpPort = "9090"
	}
	envFile := f.envFile
	if envFile == "" {
		envFile = "the node's .env"
	}
	nodeID := f.nodeID
	if nodeID == "" {
		nodeID = "<node id>"
	}
	noLeader := f.noLeaderFor.Truncate(time.Minute)
	if noLeader < time.Minute {
		noLeader = f.noLeaderFor.Truncate(time.Second)
	}

	// What the node can do for the operator, rather than instructions for them
	// to type: move itself to the address the machine really holds and ask the
	// cluster to update its record. Offered only when this node knows an
	// address DIFFERENT from the one it advertises — otherwise there is
	// nothing to move to and the fault is somewhere in the path.
	if f.localIPv4 != "" && f.localIPv4 != oldIP {
		a.Fix = hosts.NodeAlertFixReadvertise
		a.FixTarget = net.JoinHostPort(f.localIPv4, raftPort)
	}

	// Where this node's dashboard actually answers, so the UI can link to the
	// settings page of the machine that needs the fix. Prefer the node's own
	// advertised URL — it is the one that survives a reverse proxy — and fall
	// back to the address the machine really holds ONLY when that URL is
	// demonstrably the dead one (it points at the very address peers cannot
	// reach). A URL on a different host may be perfectly fine even when the
	// Raft address is stale, so don't discard it blindly.
	advertiseURL := strings.TrimRight(f.advertiseURL, "/")
	if advertiseURL != "" && !urlPointsAt(advertiseURL, oldIP) {
		a.NodeURL = advertiseURL
	} else if f.localIPv4 != "" {
		a.NodeURL = "http://" + net.JoinHostPort(f.localIPv4, httpPort)
	} else {
		a.NodeURL = advertiseURL
	}

	switch cause {
	case causeStaleAdvertise:
		a.Title = "Node advertises an address it no longer has"
		where := "that address is not on this machine"
		if f.localIPv4 != "" {
			where = fmt.Sprintf("that address is not on this machine (it is at %s now)", f.localIPv4)
		}
		a.Detail = fmt.Sprintf("This node tells the cluster to reach it at %s, but %s. Peers keep dialing the dead address, so the node has heard no leader for %s: it cannot make cluster writes and its host record (IP, uptime, kernel, …) is frozen cluster-wide. Metrics still stream to peers at a reduced rate so this card stays alive.",
			f.advertiseAddr, where, noLeader)
		if oldIP != "" {
			a.Action = fmt.Sprintf("Give the machine %s back, or move the node to %s.", oldIP, newIP)
		} else {
			a.Action = fmt.Sprintf("Move the node to %s.", newIP)
		}
	case causeUnreachable:
		a.Title = "Node cut off from its cluster"
		// Do NOT claim the address is local here: this branch also covers a
		// correct public/NAT address on a privately-addressed machine.
		a.Detail = fmt.Sprintf("Peers report a leader, but this node has heard none for %s — nobody reaches its advertised Raft address %s. The address itself doesn't look stale, so something in between is blocking it (firewall, NAT/port forward, a moved port). Until then the node cannot make cluster writes and its host record is frozen cluster-wide. Metrics still stream to peers at a reduced rate so this card stays alive.",
			noLeader, f.advertiseAddr)
		a.Action = fmt.Sprintf("Open the path to %s from the other nodes, or move the node to the address it really has.", f.advertiseAddr)
	}
	return a
}

// IsolationDetector runs on every Raft-enabled node. Each tick it reads the
// local Raft view; once the node has heard no leader for isolationDeafAfter it
// pings the catalog peers, compares its advertised IP with the machine's real
// addresses and publishes (or clears) the hosts.NodeAlert for the local
// collector row. Current() feeds the metric-stream sender and GET /raft/status.
type IsolationDetector struct {
	logger *log.Logger
	svc    Service
	db     *gorm.DB
	cfg    func() config.RaftConfig
	store  *hosts.NodeAlertStore
	client *http.Client

	// Injectable for tests.
	localIPs func(ctx context.Context) (ips []string, known bool)
	primary  func(ctx context.Context) string
	now      func() time.Time

	mu           sync.Mutex
	leaderSeenAt time.Time
	wasEnabled   bool
	since        time.Time // first detection of the current fault
	current      *hosts.NodeAlert
	lastCause    isolationCause
}

// NewIsolationDetector wires the detector over the live (swappable) Service.
func NewIsolationDetector(logger *log.Logger, svc Service, db *gorm.DB, cfg func() config.RaftConfig, store *hosts.NodeAlertStore) *IsolationDetector {
	return &IsolationDetector{
		logger:   logger,
		svc:      svc,
		db:       db,
		cfg:      cfg,
		store:    store,
		client:   &http.Client{Timeout: isolationPingTO},
		localIPs: hostnet.LocalIPv4s,
		primary:  hostnet.PrimaryIPv4,
		now:      time.Now,
	}
}

// Current returns this node's own alert, nil when healthy.
func (d *IsolationDetector) Current() *hosts.NodeAlert {
	if d == nil {
		return nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.current == nil {
		return nil
	}
	a := *d.current
	return &a
}

// Run ticks until ctx ends. One loop survives Raft re-activation: it reads the
// swappable Service live and resets its clock when Raft flips on.
func (d *IsolationDetector) Run(ctx context.Context) {
	t := time.NewTicker(isolationTick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			d.tick(ctx)
		}
	}
}

func (d *IsolationDetector) tick(ctx context.Context) {
	now := d.now()
	enabled := d.svc != nil && d.svc.Enabled()
	var st Status
	if enabled {
		st = d.svc.Status()
	}

	d.mu.Lock()
	if enabled && !d.wasEnabled {
		d.leaderSeenAt = now // (re)activation: grace period starts now
	}
	d.wasEnabled = enabled
	if !enabled || st.State == hraft.Leader.String() || st.LeaderID != "" {
		d.leaderSeenAt = now
	}
	noLeaderFor := now.Sub(d.leaderSeenAt)
	d.mu.Unlock()

	in := isolationInputs{
		enabled:     enabled,
		state:       st.State,
		leaderID:    st.LeaderID,
		noLeaderFor: noLeaderFor,
	}
	cfg := d.cfg()
	advertiseAddr := strings.TrimSpace(cfg.AdvertiseAddr)
	if advertiseAddr == "" {
		advertiseAddr = strings.TrimSpace(cfg.BindAddr)
	}
	// Only past the deaf window do we touch the network or the topology; a
	// healthy cluster never pays for this loop.
	if enabled && st.State != hraft.Leader.String() && st.LeaderID == "" && noLeaderFor >= isolationDeafAfter {
		in.advertiseIP = advertiseIPFromAddr(advertiseAddr)
		in.localIPs, in.localIPsKnown = d.localIPs(ctx)
		in.peersAnswered, in.peersWithLeader = d.probePeers(ctx, cfg.ClusterID, cfg.NodeID)
	}

	cause := decideIsolation(in, isolationDeafAfter)

	d.mu.Lock()
	defer d.mu.Unlock()
	if cause == causeNone {
		if d.current != nil {
			d.logger.Info("raft: node reconnected to its cluster — clearing isolation alert", "node_id", cfg.NodeID)
		}
		d.current, d.lastCause, d.since = nil, causeNone, time.Time{}
		d.store.Clear(hosts.LocalCollectorHostID)
		return
	}
	if d.since.IsZero() {
		d.since = now
	}
	facts := isolationFacts{
		nodeID:        cfg.NodeID,
		advertiseAddr: advertiseAddr,
		advertiseURL:  cfg.AdvertiseURL,
		localIPv4:     d.primary(ctx),
		raftPort:      portOf(advertiseAddr),
		httpPort:      httpPortHint(cfg.AdvertiseURL),
		envFile:       envFileHint(),
		noLeaderFor:   noLeaderFor,
	}
	alert := buildIsolationAlert(cause, facts, d.since)
	if d.lastCause != cause {
		d.logger.Warn("raft: this node is cut off from its cluster",
			"node_id", cfg.NodeID, "advertise", advertiseAddr, "local_ipv4", facts.localIPv4,
			"no_leader_for", noLeaderFor.Truncate(time.Second), "peers_answered", in.peersAnswered,
			"peers_with_leader", in.peersWithLeader, "cause", cause)
	}
	d.current, d.lastCause = alert, cause
	d.store.Set(hosts.LocalCollectorHostID, *alert)
}

// probePeers pings every catalog peer of this cluster and counts how many
// answered and how many of those see a leader.
func (d *IsolationDetector) probePeers(ctx context.Context, clusterID, selfNode string) (answered, withLeader int) {
	if d.db == nil {
		return 0, 0
	}
	urls, err := ListClusterPeerURLs(ctx, d.db, clusterID, selfNode)
	if err != nil || len(urls) == 0 {
		return 0, 0
	}
	type res struct{ ok, leader bool }
	out := make(chan res, len(urls))
	for _, u := range urls {
		go func(base string) {
			r := res{}
			pctx, cancel := context.WithTimeout(ctx, isolationPingTO)
			defer cancel()
			req, rerr := http.NewRequestWithContext(pctx, http.MethodGet, strings.TrimRight(base, "/")+"/api/v1/raft/ping", nil)
			if rerr == nil {
				if resp, derr := d.client.Do(req); derr == nil {
					_ = resp.Body.Close()
					if resp.StatusCode/100 == 2 {
						r.ok = true
						state := resp.Header.Get("X-Raft-State")
						r.leader = state == hraft.Leader.String() || resp.Header.Get(LeaderIDHeader) != ""
					}
				}
			}
			out <- r
		}(u)
	}
	for range urls {
		r := <-out
		if r.ok {
			answered++
			if r.leader {
				withLeader++
			}
		}
	}
	return answered, withLeader
}

// urlPointsAt reports whether rawURL's host is exactly ip — i.e. the URL leads
// to the address that is being reported as unreachable. False for an empty ip
// (nothing to compare) and for a hostname, which may resolve anywhere.
func urlPointsAt(rawURL, ip string) bool {
	if ip == "" {
		return false
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	return u.Hostname() == ip
}

func portOf(hostport string) string {
	if _, p, err := net.SplitHostPort(strings.TrimSpace(hostport)); err == nil {
		return p
	}
	return ""
}

// httpPortHint picks the HTTP port the operator should put into the new
// advertise URL: the configured advertise URL's port, else this process's
// listen port (ADDR), else the docker default.
func httpPortHint(advertiseURL string) string {
	if u, err := url.Parse(strings.TrimSpace(advertiseURL)); err == nil && u.Port() != "" {
		return u.Port()
	}
	if _, p, err := net.SplitHostPort(strings.TrimSpace(os.Getenv("ADDR"))); err == nil && p != "" {
		return p
	}
	return ""
}

// envFileHint names the file the RAFT_* / NODE_STATS_IPV4 pins live in, as the
// OPERATOR sees it: under Docker the stack dir's .env.agent (bind-mounted to
// /app/.env) plus the compose .env that carries NODE_STATS_IPV4; natively the
// runtime env file itself.
func envFileHint() string {
	if stack := strings.TrimSpace(os.Getenv("NODE_STATS_STACK_HOST_DIR")); stack != "" {
		return fmt.Sprintf("%s/.env.agent (and NODE_STATS_IPV4 also in %s/.env)", stack, stack)
	}
	if p := strings.TrimSpace(os.Getenv("NODE_STATS_ENV_FILE")); p != "" {
		return p
	}
	if wd, err := os.Getwd(); err == nil {
		return filepath.Join(wd, ".env")
	}
	return ".env"
}
