package metricstream

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"system-stats/internal/app/config"
	raftcluster "system-stats/internal/cluster/raft"
	"system-stats/internal/cluster/raft/bridge"
)

type fakeIngester struct {
	called  bool
	origin  string
	payload raftcluster.MetricBatchPayload
}

func (f *fakeIngester) Ingest(_ context.Context, p raftcluster.MetricBatchPayload, origin string) error {
	f.called = true
	f.origin = origin
	f.payload = p
	return nil
}

// serve drives one request through the receiver and returns the recorder.
func serve(r *Receiver, body []byte, tsNanos int64, sig, senderCluster string) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(http.MethodPost, Path, bytes.NewReader(body))
	if tsNanos != 0 {
		req.Header.Set(bridge.TimestampHeader, strconv.FormatInt(tsNanos, 10))
	}
	if sig != "" {
		req.Header.Set(bridge.HMACHeader, sig)
	}
	if senderCluster != "" {
		req.Header.Set(bridge.SenderClusterHeader, senderCluster)
	}
	c.Request = req
	r.Handle(c)
	return w
}

func TestReceiverIntraClusterValid(t *testing.T) {
	ing := &fakeIngester{}
	r := NewReceiver(nil, ing, "site-a", "jwtkey", config.RaftBridgeConfig{})

	body, _ := json.Marshal(raftcluster.MetricBatchPayload{HostMAC: "aa:bb", HostName: "node1"})
	ts := time.Now().UnixNano()
	sig := bridge.Sign("jwtkey", ts, body)

	w := serve(r, body, ts, sig, "site-a")
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	if !ing.called {
		t.Fatal("ingester not called")
	}
	if ing.origin != "" {
		t.Fatalf("same-cluster origin must be empty, got %q", ing.origin)
	}
	if ing.payload.HostMAC != "aa:bb" {
		t.Fatalf("payload not decoded, got %+v", ing.payload)
	}
}

func TestReceiverCrossClusterValid(t *testing.T) {
	ing := &fakeIngester{}
	r := NewReceiver(nil, ing, "site-a", "jwtkey", config.RaftBridgeConfig{SharedSecret: "bridgekey"})

	body, _ := json.Marshal(raftcluster.MetricBatchPayload{HostMAC: "cc:dd", HostName: "spoke1"})
	ts := time.Now().UnixNano()
	sig := bridge.Sign("bridgekey", ts, body) // signed with the bridge secret

	w := serve(r, body, ts, sig, "site-b") // foreign cluster
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	if ing.origin != "site-b" {
		t.Fatalf("cross-cluster origin must be the sender cluster, got %q", ing.origin)
	}
}

func TestReceiverRejectsBadSignature(t *testing.T) {
	ing := &fakeIngester{}
	r := NewReceiver(nil, ing, "site-a", "jwtkey", config.RaftBridgeConfig{})

	body, _ := json.Marshal(raftcluster.MetricBatchPayload{HostMAC: "aa:bb"})
	ts := time.Now().UnixNano()
	wrong := bridge.Sign("not-the-key", ts, body)

	w := serve(r, body, ts, wrong, "site-a")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", w.Code)
	}
	if ing.called {
		t.Fatal("ingester must NOT run on a bad signature")
	}
}

func TestReceiverRejectsForeignClusterWithoutBridgeSecret(t *testing.T) {
	ing := &fakeIngester{}
	r := NewReceiver(nil, ing, "site-a", "jwtkey", config.RaftBridgeConfig{}) // no bridge secret

	body, _ := json.Marshal(raftcluster.MetricBatchPayload{HostMAC: "cc:dd"})
	ts := time.Now().UnixNano()
	sig := bridge.Sign("jwtkey", ts, body)

	w := serve(r, body, ts, sig, "site-b") // foreign cluster, no bridge secret here
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", w.Code)
	}
	if ing.called {
		t.Fatal("ingester must NOT run for an unauthenticated foreign cluster")
	}
}

func TestReceiverRejectsMissingSenderHeader(t *testing.T) {
	ing := &fakeIngester{}
	r := NewReceiver(nil, ing, "site-a", "jwtkey", config.RaftBridgeConfig{})

	body, _ := json.Marshal(raftcluster.MetricBatchPayload{HostMAC: "aa:bb"})
	ts := time.Now().UnixNano()
	sig := bridge.Sign("jwtkey", ts, body)

	w := serve(r, body, ts, sig, "") // no sender cluster header
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", w.Code)
	}
}
