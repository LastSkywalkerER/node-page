package raft

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/log"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"system-stats/internal/app/config"
	hosts "system-stats/internal/cluster/hosts"
)

func TestDecideIsolation(t *testing.T) {
	t.Parallel()
	const deaf = 2 * time.Minute
	base := isolationInputs{
		enabled:       true,
		state:         "Candidate",
		noLeaderFor:   deaf + time.Second,
		advertiseIP:   "192.168.0.110",
		localIPs:      []string{"192.168.0.103", "172.17.0.1"},
		localIPsKnown: true,
		peersAnswered: 2,
	}
	with := func(mut func(*isolationInputs)) isolationInputs { in := base; mut(&in); return in }

	tests := []struct {
		name string
		in   isolationInputs
		want isolationCause
	}{
		// The SkyNAS case: moved machine, .env still pins the old IP, peers alive.
		{"stale advertise, peers leaderless", base, causeStaleAdvertise},
		{"stale advertise, peers have a leader", with(func(i *isolationInputs) { i.peersWithLeader = 1 }), causeStaleAdvertise},
		// Address is ours, yet the cluster has a leader we never hear: something in between.
		{"local advertise, peers have a leader", with(func(i *isolationInputs) { i.advertiseIP = "192.168.0.103"; i.peersWithLeader = 2 }), causeUnreachable},
		// Leaderless everywhere with a sane address: not this detector's business.
		{"local advertise, peers leaderless", with(func(i *isolationInputs) { i.advertiseIP = "192.168.0.103" }), causeNone},
		// Silence: cannot tell isolation from a general outage.
		{"no peer answered", with(func(i *isolationInputs) { i.peersAnswered = 0; i.peersWithLeader = 0 }), causeNone},
		// Docker without the host view: the local list is the bridge address only — abstain.
		{"local addresses unknown", with(func(i *isolationInputs) { i.localIPsKnown = false; i.localIPs = nil }), causeNone},
		{"local addresses unknown but peers lead", with(func(i *isolationInputs) { i.localIPsKnown = false; i.peersWithLeader = 1 }), causeUnreachable},
		// DNS-name / bare-port advertise: nothing to compare; fall back to the peer signal.
		{"hostname advertise, peers lead", with(func(i *isolationInputs) { i.advertiseIP = ""; i.peersWithLeader = 1 }), causeUnreachable},
		{"hostname advertise, peers leaderless", with(func(i *isolationInputs) { i.advertiseIP = "" }), causeNone},
		// Healthy states never alert, whatever else looks off.
		{"leader", with(func(i *isolationInputs) { i.state = "Leader" }), causeNone},
		{"follower with a leader", with(func(i *isolationInputs) { i.state = "Follower"; i.leaderID = "node-stats" }), causeNone},
		{"raft disabled", with(func(i *isolationInputs) { i.enabled = false }), causeNone},
		{"not deaf long enough", with(func(i *isolationInputs) { i.noLeaderFor = deaf - time.Second }), causeNone},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := decideIsolation(tc.in, deaf); got != tc.want {
				t.Fatalf("decideIsolation = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestAdvertiseIPFromAddr(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"192.168.0.110:7000":    "192.168.0.110",
		" 65.21.152.83:7001 ":   "65.21.152.83",
		"[fe80::1]:7000":        "fe80::1",
		":7000":                 "",
		"0.0.0.0:7000":          "",
		"raft.example.org:7000": "",
		"":                      "",
	} {
		if got := advertiseIPFromAddr(in); got != want {
			t.Errorf("advertiseIPFromAddr(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBuildIsolationAlert_StaleAdvertise(t *testing.T) {
	t.Parallel()
	a := buildIsolationAlert(causeStaleAdvertise, isolationFacts{
		nodeID:        "skynas",
		advertiseAddr: "192.168.0.110:7000",
		advertiseURL:  "http://192.168.0.110:9090",
		localIPv4:     "192.168.0.103",
		raftPort:      "7000",
		httpPort:      "9090",
		envFile:       "/opt/node-stats/.env.agent",
		noLeaderFor:   20 * time.Hour,
	}, time.Now())
	if a == nil || a.Kind != hosts.NodeAlertRaftIsolated || a.Severity != hosts.NodeAlertSeverityError {
		t.Fatalf("alert = %+v", a)
	}
	joined := a.Title + "\n" + a.Detail + "\n" + strings.Join(a.Steps, "\n")
	for _, want := range []string{
		"192.168.0.110:7000",                     // what it advertises
		"it is at 192.168.0.103 now",             // where it really is
		"old address 192.168.0.110 back",         // option 1: DHCP reservation
		"RAFT_ADVERTISE_ADDR=192.168.0.103:7000", // option 2: re-advertise
		"RAFT_ADVERTISE_PUBLIC_URL=http://192.168.0.103:9090",
		"NODE_STATS_IPV4=192.168.0.103",
		"/opt/node-stats/.env.agent",
		`id "skynas" and address 192.168.0.103:7000`, // option 2b: add the voter on the leader
		`"Advanced: manually add a voter"`,           // the real UI path, not an invented one
		"reduced rate",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("alert text lacks %q:\n%s", want, joined)
		}
	}
	if len(a.Steps) != 3 {
		t.Fatalf("want 3 steps (DHCP, re-advertise, add peer), got %d: %v", len(a.Steps), a.Steps)
	}
	if buildIsolationAlert(causeNone, isolationFacts{}, time.Now()) != nil {
		t.Fatal("causeNone must yield no alert")
	}
}

func TestBuildIsolationAlert_Unreachable(t *testing.T) {
	t.Parallel()
	a := buildIsolationAlert(causeUnreachable, isolationFacts{
		nodeID: "valheim", advertiseAddr: "65.21.152.83:7001", noLeaderFor: 3 * time.Minute,
	}, time.Now())
	if a == nil || !strings.Contains(a.Detail, "65.21.152.83:7001") || !strings.Contains(a.Title, "cut off") {
		t.Fatalf("alert = %+v", a)
	}
	joined := strings.Join(a.Steps, "\n")
	// The steps must name controls the admin UI actually has.
	for _, want := range []string{`"Raft cluster sync"`, `"Probe"`, `"Add voter"`} {
		if !strings.Contains(joined, want) {
			t.Errorf("unreachable steps lack the real UI control %s: %v", want, a.Steps)
		}
	}
}

// isoSvc is a Service whose view the test flips between "candidate with no
// leader" and "follower with a leader".
type isoSvc struct {
	DisabledService
	mu      sync.Mutex
	enabled bool
	st      Status
}

func (s *isoSvc) Enabled() bool  { s.mu.Lock(); defer s.mu.Unlock(); return s.enabled }
func (s *isoSvc) Status() Status { s.mu.Lock(); defer s.mu.Unlock(); return s.st }
func (s *isoSvc) set(enabled bool, st Status) {
	s.mu.Lock()
	s.enabled, s.st = enabled, st
	s.mu.Unlock()
}

// TestIsolationDetector_EndToEnd drives the detector through the SkyNAS
// timeline: healthy follower → leader lost → deaf window elapses → peers (one
// leading) answer /raft/ping → the alert is published for the local row with
// the stale-advertise diagnosis → the leader is heard again → alert cleared.
func TestIsolationDetector_EndToEnd(t *testing.T) {
	t.Parallel()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("sqlite: %v", err)
	}
	if err := db.AutoMigrate(&peerNodeAdvertise{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	leader := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Raft-State", "Leader")
		w.Header().Set(LeaderIDHeader, "node-stats")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer leader.Close()
	follower := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Raft-State", "Follower")
		w.Header().Set(LeaderIDHeader, "node-stats")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer follower.Close()
	for node, u := range map[string]string{"node-stats": leader.URL, "orangepi5-plus": follower.URL} {
		if err := db.Create(&peerNodeAdvertise{ClusterID: "sky-home", NodeID: node, URL: u}).Error; err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	svc := &isoSvc{}
	svc.set(true, Status{Enabled: true, State: "Follower", LeaderID: "node-stats"})
	store := hosts.NewNodeAlertStore()
	cfg := config.RaftConfig{ClusterID: "sky-home", NodeID: "skynas", AdvertiseAddr: "192.168.0.110:7000", AdvertiseURL: "http://192.168.0.110:9090"}
	d := NewIsolationDetector(log.New(io.Discard), svc, db, func() config.RaftConfig { return cfg }, store)
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	d.now = func() time.Time { return now }
	d.localIPs = func(context.Context) ([]string, bool) { return []string{"192.168.0.103", "172.27.0.1"}, true }
	d.primary = func(context.Context) string { return "192.168.0.103" }
	ctx := context.Background()

	d.tick(ctx)
	if d.Current() != nil {
		t.Fatal("healthy follower raised an alert")
	}

	// Leader lost (machine moved; peers dial the dead address). Inside the deaf
	// window nothing happens yet — elections and restarts are normal noise.
	svc.set(true, Status{Enabled: true, State: "Candidate"})
	now = now.Add(isolationDeafAfter - time.Second)
	d.tick(ctx)
	if d.Current() != nil {
		t.Fatal("alert raised before the deaf window elapsed")
	}

	now = now.Add(2 * time.Second)
	d.tick(ctx)
	a := d.Current()
	if a == nil {
		t.Fatal("no alert after the deaf window with peers leading")
	}
	if a.AdvertiseAddr != "192.168.0.110:7000" || a.LocalIPv4 != "192.168.0.103" || a.NodeID != "skynas" {
		t.Fatalf("alert facts = %+v", a)
	}
	if !strings.Contains(a.Title, "no longer has") {
		t.Fatalf("expected the stale-advertise diagnosis, got title %q", a.Title)
	}
	if !a.Since.Equal(now) {
		t.Fatalf("since = %v, want first detection %v", a.Since, now)
	}
	if got := store.NodeAlert(hosts.LocalCollectorHostID); got == nil || got.Title != a.Title {
		t.Fatalf("local row alert not in the store: %+v", got)
	}

	// Still isolated a tick later: same episode, `since` must not move.
	now = now.Add(isolationTick)
	d.tick(ctx)
	if b := d.Current(); b == nil || !b.Since.Equal(a.Since) {
		t.Fatalf("since drifted across ticks: %+v", b)
	}

	// Operator fixed the address; the leader's heartbeats arrive again.
	svc.set(true, Status{Enabled: true, State: "Follower", LeaderID: "node-stats"})
	now = now.Add(isolationTick)
	d.tick(ctx)
	if d.Current() != nil {
		t.Fatal("alert not cleared once the leader was heard")
	}
	if store.NodeAlert(hosts.LocalCollectorHostID) != nil {
		t.Fatal("store still holds the local alert after recovery")
	}

	// Raft switched off entirely (leave / factory reset): silent.
	svc.set(false, Status{})
	now = now.Add(time.Hour)
	d.tick(ctx)
	if d.Current() != nil {
		t.Fatal("disabled raft raised an alert")
	}
}

// TestIsolationDetector_NoPeersAnswerStaysSilent: a leaderless node whose
// peers are all dark (or whose catalog is empty) cannot tell isolation from a
// cluster-wide outage and must not accuse its own address.
func TestIsolationDetector_NoPeersAnswerStaysSilent(t *testing.T) {
	t.Parallel()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("sqlite: %v", err)
	}
	if err := db.AutoMigrate(&peerNodeAdvertise{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	dead.Close() // connection refused from now on
	if err := db.Create(&peerNodeAdvertise{ClusterID: "c", NodeID: "peer", URL: dead.URL}).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}

	svc := &isoSvc{}
	svc.set(true, Status{Enabled: true, State: "Candidate"})
	cfg := config.RaftConfig{ClusterID: "c", NodeID: "me", AdvertiseAddr: "192.0.2.10:7000"}
	d := NewIsolationDetector(log.New(io.Discard), svc, db, func() config.RaftConfig { return cfg }, hosts.NewNodeAlertStore())
	now := time.Now()
	d.now = func() time.Time { return now }
	d.localIPs = func(context.Context) ([]string, bool) { return []string{"10.0.0.5"}, true }
	d.primary = func(context.Context) string { return "10.0.0.5" }

	d.tick(context.Background())
	now = now.Add(isolationDeafAfter + time.Second)
	d.tick(context.Background())
	if a := d.Current(); a != nil {
		t.Fatalf("alert raised with no peer answering: %+v", a)
	}
}

// TestDecideIsolation_NATAdvertiseIsNotCalledStale: a node behind NAT (or a
// cloud VM) legitimately advertises a public address that is on no local
// interface. That must not read as "you pinned the wrong IP" — the value is
// right and the path is broken, so it is reported as unreachable instead.
func TestDecideIsolation_NATAdvertiseIsNotCalledStale(t *testing.T) {
	t.Parallel()
	const deaf = 2 * time.Minute
	nat := isolationInputs{
		enabled: true, state: "Candidate", noLeaderFor: deaf + time.Second,
		advertiseIP: "65.21.152.83", localIPs: []string{"10.0.12.3", "172.17.0.1"},
		localIPsKnown: true, peersAnswered: 1, peersWithLeader: 1,
	}
	if got := decideIsolation(nat, deaf); got != causeUnreachable {
		t.Fatalf("NAT/public advertise = %v, want causeUnreachable", got)
	}
	// With no peer reporting a leader there is nothing to conclude at all.
	quiet := nat
	quiet.peersWithLeader = 0
	if got := decideIsolation(quiet, deaf); got != causeNone {
		t.Fatalf("NAT advertise with leaderless peers = %v, want causeNone", got)
	}
	// A machine that DOES hold a public address is compared honestly: a public
	// advertise that isn't the one it has is stale like any other.
	dual := nat
	dual.localIPs = []string{"65.21.152.90", "10.0.12.3"}
	dual.peersWithLeader = 0
	if got := decideIsolation(dual, deaf); got != causeStaleAdvertise {
		t.Fatalf("public-addressed machine with a wrong public advertise = %v, want causeStaleAdvertise", got)
	}
	// CGNAT / Tailscale addresses count as private too.
	cg := nat
	cg.localIPs = []string{"100.87.3.4"}
	if got := decideIsolation(cg, deaf); got != causeUnreachable {
		t.Fatalf("CGNAT-only machine = %v, want causeUnreachable", got)
	}
}
