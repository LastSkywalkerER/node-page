package raft

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	hosts "system-stats/internal/cluster/hosts"
)

type pingSvc struct {
	DisabledService
	st Status
}

func (p pingSvc) Status() Status { return p.st }
func (p pingSvc) Enabled() bool  { return true }

// TestPingExposesLeaderID: /raft/ping tells the caller which leader this peer
// follows, which is what an isolated node needs to tell "the cluster has a
// leader I can't hear" apart from "the cluster has no leader".
func TestPingExposesLeaderID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := NewHandler(pingSvc{st: Status{Enabled: true, ClusterID: "sky-home", NodeID: "orangepi", State: "Follower", LeaderID: "node-stats"}})
	r := gin.New()
	r.GET("/ping", h.Ping)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ping", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d", rec.Code)
	}
	if got := rec.Header().Get(LeaderIDHeader); got != "node-stats" {
		t.Fatalf("%s = %q, want node-stats", LeaderIDHeader, got)
	}
	if got := rec.Header().Get("X-Raft-State"); got != "Follower" {
		t.Fatalf("X-Raft-State = %q", got)
	}
}

// TestStatusCarriesIsolation: the admin status payload surfaces the node's own
// isolation diagnosis when there is one and omits the key otherwise.
func TestStatusCarriesIsolation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var alert *hosts.NodeAlert
	h := NewHandler(pingSvc{st: Status{Enabled: true}}).WithIsolationSource(func() *hosts.NodeAlert { return alert })
	r := gin.New()
	r.GET("/status", h.Status)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/status", nil))
	if body := rec.Body.String(); strings.Contains(body, `"isolation"`) {
		t.Fatalf("healthy status carried an isolation key: %s", body)
	}

	alert = &hosts.NodeAlert{Kind: hosts.NodeAlertRaftIsolated, Title: "Node cut off from its cluster"}
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/status", nil))
	if body := rec.Body.String(); !strings.Contains(body, `"isolation"`) || !strings.Contains(body, "Node cut off from its cluster") {
		t.Fatalf("status missing the isolation alert: %s", body)
	}
}
