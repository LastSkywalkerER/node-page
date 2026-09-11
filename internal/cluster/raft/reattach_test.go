package raft

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/charmbracelet/log"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestReattachPayloadValidate(t *testing.T) {
	t.Parallel()
	ok := ReattachPayload{ClusterID: "c", NodeID: "n", RaftAddr: "10.0.0.5:7000"}
	if err := ok.Validate(); err != nil {
		t.Fatalf("valid payload rejected: %v", err)
	}
	for name, p := range map[string]ReattachPayload{
		"no cluster":  {NodeID: "n", RaftAddr: "10.0.0.5:7000"},
		"no node":     {ClusterID: "c", RaftAddr: "10.0.0.5:7000"},
		"no port":     {ClusterID: "c", NodeID: "n", RaftAddr: "10.0.0.5"},
		"no host":     {ClusterID: "c", NodeID: "n", RaftAddr: ":7000"},
		"bad port":    {ClusterID: "c", NodeID: "n", RaftAddr: "10.0.0.5:0"},
		"empty":       {},
		"not an addr": {ClusterID: "c", NodeID: "n", RaftAddr: "nonsense"},
	} {
		if err := p.Validate(); err == nil {
			t.Errorf("%s: expected a rejection, got none", name)
		}
	}
}

// reattachSvc is a Service with a settable role that records AddVoter calls.
type reattachSvc struct {
	DisabledService
	st     Status
	leader bool
	added  [][2]string
	addErr error
}

func (s *reattachSvc) Enabled() bool  { return true }
func (s *reattachSvc) IsLeader() bool { return s.leader }
func (s *reattachSvc) Status() Status { return s.st }
func (s *reattachSvc) AddVoter(id, addr string) error {
	if s.addErr != nil {
		return s.addErr
	}
	s.added = append(s.added, [2]string{id, addr})
	return nil
}

func reattachRouter(h *Handler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST(reattachPath, h.ClusterReattach)
	return r
}

func signedReattachRequest(t *testing.T, secret string, p ReattachPayload, hop int) *http.Request {
	t.Helper()
	body, _ := json.Marshal(p)
	req := httptest.NewRequest(http.MethodPost, reattachPath, bytes.NewReader(body))
	ts := int64(1789000000000000000)
	req.Header.Set(ForwardTimestampHeader, strconv.FormatInt(ts, 10))
	if secret != "" {
		req.Header.Set(ForwardSignatureHeader, signForward(secret, ts, body))
	}
	req.Header.Set(reattachHopHeader, strconv.Itoa(hop))
	return req
}

// TestClusterReattach_LeaderAppliesOnlySignedRequests: the endpoint moves a
// voter's address, so an unsigned or foreign-cluster call must change nothing.
func TestClusterReattach_LeaderAppliesOnlySignedRequests(t *testing.T) {
	t.Parallel()
	const secret = "cluster-shared-secret"
	svc := &reattachSvc{leader: true, st: Status{Enabled: true, ClusterID: "demo", NodeID: "hub"}}
	h := NewHandler(svc).WithForwardSecret(func() string { return secret })
	r := reattachRouter(h)
	p := ReattachPayload{ClusterID: "demo", NodeID: "edge", RaftAddr: "172.21.0.4:7000", HTTPURL: "http://172.21.0.4:9090"}

	// Unsigned: refused.
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, signedReattachRequest(t, "", p, 0))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unsigned request got %d, want 401", rec.Code)
	}
	// Signed with the wrong key: refused.
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, signedReattachRequest(t, "not-the-secret", p, 0))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrongly-signed request got %d, want 401", rec.Code)
	}
	// Another cluster's node: refused.
	foreign := p
	foreign.ClusterID = "someone-else"
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, signedReattachRequest(t, secret, foreign, 0))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("foreign cluster got %d, want 403", rec.Code)
	}
	// A node asking about itself: refused (it would be its own membership).
	self := p
	self.NodeID = "hub"
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, signedReattachRequest(t, secret, self, 0))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("self-request got %d, want 400", rec.Code)
	}
	if len(svc.added) != 0 {
		t.Fatalf("a refused request still changed membership: %v", svc.added)
	}

	// Properly signed: applied in place, keeping the node id.
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, signedReattachRequest(t, secret, p, 0))
	if rec.Code != http.StatusOK {
		t.Fatalf("signed request got %d: %s", rec.Code, rec.Body.String())
	}
	if len(svc.added) != 1 || svc.added[0] != [2]string{"edge", "172.21.0.4:7000"} {
		t.Fatalf("membership change = %v", svc.added)
	}
}

// TestClusterReattach_FollowerForwardsOnce: a node that is not the leader
// passes the request on to the leader, and a request that has already been
// forwarded is refused rather than bounced around the cluster.
func TestClusterReattach_FollowerForwardsOnce(t *testing.T) {
	t.Parallel()
	const secret = "cluster-shared-secret"

	leaderSvc := &reattachSvc{leader: true, st: Status{Enabled: true, ClusterID: "demo", NodeID: "hub"}}
	leaderSrv := httptest.NewServer(reattachRouter(NewHandler(leaderSvc).WithForwardSecret(func() string { return secret })))
	defer leaderSrv.Close()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("sqlite: %v", err)
	}
	if err := db.AutoMigrate(&peerNodeAdvertise{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := db.Create(&peerNodeAdvertise{ClusterID: "demo", NodeID: "hub", URL: leaderSrv.URL}).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}

	follower := &reattachSvc{st: Status{Enabled: true, ClusterID: "demo", NodeID: "peer", LeaderID: "hub"}}
	fh := NewHandler(follower).
		WithDeps(nil, db, log.New(io.Discard), "demo").
		WithForwardSecret(func() string { return secret })
	r := reattachRouter(fh)
	p := ReattachPayload{ClusterID: "demo", NodeID: "edge", RaftAddr: "172.21.0.4:7000"}

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, signedReattachRequest(t, secret, p, 0))
	if rec.Code != http.StatusOK {
		t.Fatalf("follower did not forward: %d %s", rec.Code, rec.Body.String())
	}
	if len(leaderSvc.added) != 1 || leaderSvc.added[0][1] != "172.21.0.4:7000" {
		t.Fatalf("leader did not apply the forwarded change: %v", leaderSvc.added)
	}
	if len(follower.added) != 0 {
		t.Fatalf("a follower applied a membership change itself: %v", follower.added)
	}

	// Already forwarded once: refused, so two followers can't ping-pong it.
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, signedReattachRequest(t, secret, p, 1))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("second hop got %d, want 503", rec.Code)
	}
}

// TestAskPeersToReattach_TriesEveryPeer: the asking node keeps going until one
// peer applies the change, and says what stood in the way when none can.
func TestAskPeersToReattach_TriesEveryPeer(t *testing.T) {
	t.Parallel()
	const secret = "cluster-shared-secret"
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":"not the leader"}`, http.StatusServiceUnavailable)
	}))
	defer dead.Close()
	applied := make(chan ReattachPayload, 1)
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var p ReattachPayload
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &p)
		if r.Header.Get(ForwardSignatureHeader) == "" {
			http.Error(w, "unsigned", http.StatusUnauthorized)
			return
		}
		applied <- p
		w.WriteHeader(http.StatusOK)
	}))
	defer good.Close()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("sqlite: %v", err)
	}
	if err := db.AutoMigrate(&peerNodeAdvertise{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// "a" sorts before "b": the dead peer is tried first.
	for id, u := range map[string]string{"a-dead": dead.URL, "b-good": good.URL} {
		if err := db.Create(&peerNodeAdvertise{ClusterID: "demo", NodeID: id, URL: u}).Error; err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	p := ReattachPayload{ClusterID: "demo", NodeID: "edge", RaftAddr: "172.21.0.4:7000"}
	if err := AskPeersToReattach(context.Background(), log.New(io.Discard), db, nil, secret, p); err != nil {
		t.Fatalf("ask: %v", err)
	}
	select {
	case got := <-applied:
		if got.RaftAddr != p.RaftAddr || got.NodeID != p.NodeID {
			t.Fatalf("peer received %+v", got)
		}
	default:
		t.Fatal("no peer received the request")
	}

	// No peer known at all: a clear error rather than a silent success.
	empty, _ := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Discard})
	_ = empty.AutoMigrate(&peerNodeAdvertise{})
	if err := AskPeersToReattach(context.Background(), log.New(io.Discard), empty, nil, secret, p); err == nil {
		t.Fatal("expected an error when there is no peer to ask")
	}
	// No secret: refuse to send an unsigned membership change.
	if err := AskPeersToReattach(context.Background(), log.New(io.Discard), db, nil, "", p); err == nil {
		t.Fatal("expected a refusal to send an unsigned request")
	}
}
