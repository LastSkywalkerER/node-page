package raft

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func newForwardTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&peerNodeAdvertise{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func seedAdvertise(t *testing.T, db *gorm.DB, cluster, node string, caps string) {
	t.Helper()
	row := peerNodeAdvertise{ClusterID: cluster, NodeID: node, URL: "http://" + node, Capabilities: caps, UpdatedAt: time.Now()}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("seed advertise: %v", err)
	}
}

// leaderSvc is a Service that is enabled, leader, with a fixed peer set.
type leaderSvc struct {
	fakeMemSvc
	submitted int
}

func (l *leaderSvc) SubmitCommand(context.Context, Command, time.Duration) (SubmitResult, error) {
	l.submitted++
	return SubmitResult{Index: 1, Applied: true}, nil
}

func newLeaderSvc(cluster string, voters ...string) *leaderSvc {
	peers := make([]Peer, 0, len(voters))
	for _, v := range voters {
		peers = append(peers, Peer{ID: v, Suffrage: "voter"})
	}
	l := &leaderSvc{}
	l.leader = true
	l.status = Status{Enabled: true, ClusterID: cluster, Peers: peers}
	return l
}

func doForward(t *testing.T, h *Handler, body []byte, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest("POST", "/api/v1/raft/forward", bytes.NewReader(body))
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	c.Request = req
	h.Forward(c)
	return w
}

func cmdBody(t *testing.T) []byte {
	t.Helper()
	b, _ := json.Marshal(Command{Type: CmdUserUpsert})
	return b
}

// A signed forward is accepted whether or not the cluster is fully upgraded.
func TestForwardAcceptsValidSignature(t *testing.T) {
	db := newForwardTestDB(t)
	svc := newLeaderSvc("c1", "n1")
	h := NewHandler(svc).WithDeps(nil, db, nil, "c1").WithForwardSecret(func() string { return "sek" })

	body := cmdBody(t)
	ts := time.Now().UnixNano()
	w := doForward(t, h, body, map[string]string{
		ForwardTimestampHeader: strconv.FormatInt(ts, 10),
		ForwardClusterHeader:   "c1",
		ForwardSignatureHeader: signForward("sek", ts, body),
	})
	if w.Code != 200 {
		t.Fatalf("valid signed forward must be accepted, got %d: %s", w.Code, w.Body.String())
	}
	if svc.submitted != 1 {
		t.Fatalf("command must be submitted once, got %d", svc.submitted)
	}
}

func TestForwardRejectsBadSignature(t *testing.T) {
	db := newForwardTestDB(t)
	svc := newLeaderSvc("c1", "n1")
	h := NewHandler(svc).WithDeps(nil, db, nil, "c1").WithForwardSecret(func() string { return "sek" })
	body := cmdBody(t)
	ts := time.Now().UnixNano()
	w := doForward(t, h, body, map[string]string{
		ForwardTimestampHeader: strconv.FormatInt(ts, 10),
		ForwardSignatureHeader: signForward("WRONG", ts, body),
	})
	if w.Code != 401 {
		t.Fatalf("bad signature must be 401, got %d", w.Code)
	}
	if svc.submitted != 0 {
		t.Fatal("a rejected forward must not submit")
	}
}

// Unsigned forward: rejected once EVERY voter advertises the capability.
func TestForwardRejectsUnsignedWhenFullyUpgraded(t *testing.T) {
	db := newForwardTestDB(t)
	seedAdvertise(t, db, "c1", "n1", CapForwardHMAC)
	seedAdvertise(t, db, "c1", "n2", CapForwardHMAC)
	svc := newLeaderSvc("c1", "n1", "n2")
	h := NewHandler(svc).WithDeps(nil, db, nil, "c1").WithForwardSecret(func() string { return "sek" })

	w := doForward(t, h, cmdBody(t), nil) // no signature headers
	if w.Code != 401 {
		t.Fatalf("unsigned forward must be rejected when all voters are capable, got %d", w.Code)
	}
}

// Unsigned forward: TOLERATED while some voter hasn't advertised the capability
// (a rolling upgrade / a freshly-joined voter with no advertise row yet).
func TestForwardAcceptsUnsignedDuringRollingUpgrade(t *testing.T) {
	db := newForwardTestDB(t)
	seedAdvertise(t, db, "c1", "n1", CapForwardHMAC)
	// n2 is an old node — advertises no capability.
	seedAdvertise(t, db, "c1", "n2", "")
	svc := newLeaderSvc("c1", "n1", "n2")
	h := NewHandler(svc).WithDeps(nil, db, nil, "c1").WithForwardSecret(func() string { return "sek" })

	w := doForward(t, h, cmdBody(t), nil)
	if w.Code != 200 {
		t.Fatalf("unsigned forward must be tolerated mid-upgrade, got %d: %s", w.Code, w.Body.String())
	}
}

// A voter with no advertise row at all → not-capable → permissive.
func TestForwardPermissiveWhenVoterUnknown(t *testing.T) {
	db := newForwardTestDB(t)
	seedAdvertise(t, db, "c1", "n1", CapForwardHMAC)
	svc := newLeaderSvc("c1", "n1", "n2") // n2 has no advertise row
	h := NewHandler(svc).WithDeps(nil, db, nil, "c1").WithForwardSecret(func() string { return "sek" })

	w := doForward(t, h, cmdBody(t), nil)
	if w.Code != 200 {
		t.Fatalf("a voter with no advertise row must keep the cluster permissive, got %d", w.Code)
	}
}

// A forward that names a foreign cluster is rejected outright.
func TestForwardRejectsForeignCluster(t *testing.T) {
	db := newForwardTestDB(t)
	svc := newLeaderSvc("c1", "n1")
	h := NewHandler(svc).WithDeps(nil, db, nil, "c1").WithForwardSecret(func() string { return "sek" })
	body := cmdBody(t)
	ts := time.Now().UnixNano()
	w := doForward(t, h, body, map[string]string{
		ForwardClusterHeader:   "OTHER",
		ForwardTimestampHeader: strconv.FormatInt(ts, 10),
		ForwardSignatureHeader: signForward("sek", ts, body),
	})
	if w.Code != 401 {
		t.Fatalf("foreign-cluster forward must be 401, got %d", w.Code)
	}
}

// Non-leader forwards are refused before any auth work.
func TestForwardNonLeader(t *testing.T) {
	svc := newLeaderSvc("c1", "n1")
	svc.leader = false
	h := NewHandler(svc).WithDeps(nil, newForwardTestDB(t), nil, "c1")
	w := doForward(t, h, cmdBody(t), nil)
	if w.Code != 503 {
		t.Fatalf("non-leader must return 503, got %d", w.Code)
	}
}
