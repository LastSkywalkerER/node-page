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
	// The remedy is an OFFER the node can carry out, not instructions to type:
	// no env lines, no menu paths, no wall of steps.
	if a.Fix != hosts.NodeAlertFixReadvertise || a.FixTarget != "192.168.0.103:7000" {
		t.Fatalf("expected an offer to move to the address the machine has, got fix=%q target=%q", a.Fix, a.FixTarget)
	}
	text := a.Title + "\n" + a.Detail + "\n" + a.Action
	for _, forbidden := range []string{"RAFT_ADVERTISE_ADDR", ".env", "Admin →", "Add voter"} {
		if strings.Contains(text, forbidden) {
			t.Errorf("the alert must not tell the operator to edit config by hand; found %q in:\n%s", forbidden, text)
		}
	}
	for _, want := range []string{"192.168.0.110", "192.168.0.103"} {
		if !strings.Contains(text, want) {
			t.Errorf("alert text lacks %q:\n%s", want, text)
		}
	}
	if len(a.Action) > 160 {
		t.Fatalf("action must stay one short sentence, got %d chars: %q", len(a.Action), a.Action)
	}
	if buildIsolationAlert(causeNone, isolationFacts{}, time.Now()) != nil {
		t.Fatal("causeNone must yield no alert")
	}
}

// TestBuildIsolationAlert_NoFixWhenNothingToMoveTo: when the node's own
// address IS what it advertises, moving it somewhere would be a lie — the
// fault is in the path, so no button is offered.
func TestBuildIsolationAlert_NoFixWhenNothingToMoveTo(t *testing.T) {
	t.Parallel()
	a := buildIsolationAlert(causeUnreachable, isolationFacts{
		nodeID: "valheim", advertiseAddr: "10.0.0.5:7000", localIPv4: "10.0.0.5", raftPort: "7000",
	}, time.Now())
	if a.Fix != "" || a.FixTarget != "" {
		t.Fatalf("no move to offer, yet fix=%q target=%q", a.Fix, a.FixTarget)
	}
}

func TestBuildIsolationAlert_Unreachable(t *testing.T) {
	t.Parallel()
	a := buildIsolationAlert(causeUnreachable, isolationFacts{
		nodeID: "valheim", advertiseAddr: "65.21.152.83:7001", localIPv4: "10.0.12.3",
		raftPort: "7001", noLeaderFor: 3 * time.Minute,
	}, time.Now())
	if a == nil || !strings.Contains(a.Detail, "65.21.152.83:7001") || !strings.Contains(a.Title, "cut off") {
		t.Fatalf("alert = %+v", a)
	}
	// A machine whose real address differs from the advertised one can still
	// be offered the move, whatever the cause.
	if a.Fix != hosts.NodeAlertFixReadvertise || a.FixTarget != "10.0.12.3:7001" {
		t.Fatalf("fix=%q target=%q", a.Fix, a.FixTarget)
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

// TestBuildIsolationAlert_ActionAndNodeURL pins what the COMPACT surfaces use:
// one short action sentence, and a link that reaches the node's own settings.
// A stale advertise must never be linked through the very URL it is stale on.
func TestBuildIsolationAlert_ActionAndNodeURL(t *testing.T) {
	t.Parallel()
	stale := buildIsolationAlert(causeStaleAdvertise, isolationFacts{
		nodeID: "skynas", advertiseAddr: "192.168.0.110:7000",
		advertiseURL: "http://192.168.0.110:9090", localIPv4: "192.168.0.103",
		raftPort: "7000", httpPort: "9090",
	}, time.Now())
	if stale.NodeURL != "http://192.168.0.103:9090" {
		t.Fatalf("stale advertise must link through the address the machine really has, got %q", stale.NodeURL)
	}
	if !strings.Contains(stale.Action, "192.168.0.110") || !strings.Contains(stale.Action, "192.168.0.103") {
		t.Fatalf("action should name both the old and the new address: %q", stale.Action)
	}
	if len(stale.Action) > 160 {
		t.Fatalf("action must stay one short sentence, got %d chars: %q", len(stale.Action), stale.Action)
	}

	// A stale RAFT address does not condemn a dashboard URL on a DIFFERENT
	// host — a reverse-proxied node keeps answering there.
	proxied := buildIsolationAlert(causeStaleAdvertise, isolationFacts{
		nodeID: "skynas", advertiseAddr: "192.168.0.110:7000",
		advertiseURL: "https://dash.example.com", localIPv4: "192.168.0.103", httpPort: "9090",
	}, time.Now())
	if proxied.NodeURL != "https://dash.example.com" {
		t.Fatalf("a dashboard URL on another host must be kept, got %q", proxied.NodeURL)
	}

	// Address isn't the problem → the node's own advertised URL is the better
	// (reverse-proxy aware) link.
	unreach := buildIsolationAlert(causeUnreachable, isolationFacts{
		nodeID: "valheim", advertiseAddr: "65.21.152.83:7001",
		advertiseURL: "https://dashboard.example.com", localIPv4: "10.0.12.3", httpPort: "9090",
	}, time.Now())
	if unreach.NodeURL != "https://dashboard.example.com" {
		t.Fatalf("unreachable should keep the node's advertised URL, got %q", unreach.NodeURL)
	}
	if unreach.Action == "" || !strings.Contains(unreach.Action, "65.21.152.83:7001") {
		t.Fatalf("action should name the blocked address: %q", unreach.Action)
	}

	// Nothing to build a URL from → no link rather than a wrong one.
	blind := buildIsolationAlert(causeUnreachable, isolationFacts{nodeID: "x", advertiseAddr: "host:7000"}, time.Now())
	if blind.NodeURL != "" {
		t.Fatalf("with no known address the alert must carry no link, got %q", blind.NodeURL)
	}
}
