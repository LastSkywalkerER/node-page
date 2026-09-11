package metricstream

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/charmbracelet/log"

	"system-stats/internal/app/config"
	hosts "system-stats/internal/cluster/hosts"
	raftcluster "system-stats/internal/cluster/raft"
	"system-stats/internal/cluster/raft/bridge"
)

// TestSenderIsolatedThrottlesAndStampsAlert: while the node reports itself cut
// off from its cluster, Broadcast ships at most one batch per
// isolatedShipInterval and every shipped batch carries the alert; once the
// alert clears, every tick ships again and the batch carries none.
func TestSenderIsolatedThrottlesAndStampsAlert(t *testing.T) {
	var posts atomic.Int32
	bodies := make(chan raftcluster.MetricBatchPayload, 8)
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		plain, err := bridge.Gunzip(r.Header.Get(bridge.EncodingHeader), raw)
		if err != nil {
			t.Errorf("gunzip: %v", err)
		}
		var p raftcluster.MetricBatchPayload
		_ = json.Unmarshal(plain, &p)
		posts.Add(1)
		bodies <- p
		w.WriteHeader(http.StatusOK)
	}))
	defer hub.Close()

	s := NewSender(log.New(io.Discard), nil, "spoke", "n1", "", config.RaftBridgeConfig{
		Mode: config.BridgeModePush, SharedSecret: "k", RemoteSeeds: []string{hub.URL},
	})
	var alert atomic.Pointer[hosts.NodeAlert]
	s.SetNodeAlertSource(func() *hosts.NodeAlert { return alert.Load() })

	waitPosts := func(want int32) {
		deadline := time.Now().Add(2 * time.Second)
		for posts.Load() < want && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
		if got := posts.Load(); got != want {
			t.Fatalf("posts = %d, want %d", got, want)
		}
	}

	// Healthy: two consecutive ticks both ship, without an alert.
	s.Broadcast(context.Background(), raftcluster.MetricBatchPayload{HostMAC: "x"})
	s.Broadcast(context.Background(), raftcluster.MetricBatchPayload{HostMAC: "x"})
	waitPosts(2)
	for i := 0; i < 2; i++ {
		if p := <-bodies; p.NodeAlert != nil {
			t.Fatalf("healthy batch carried an alert: %+v", p.NodeAlert)
		}
	}

	// Isolated: the alert rides the batch and the next tick inside the
	// interval is dropped.
	alert.Store(&hosts.NodeAlert{Kind: hosts.NodeAlertRaftIsolated, Title: "cut off"})
	s.lastShipAt.Store(0) // the healthy ships above must not count as "recent"
	s.Broadcast(context.Background(), raftcluster.MetricBatchPayload{HostMAC: "x"})
	waitPosts(3)
	if p := <-bodies; p.NodeAlert == nil || p.NodeAlert.Title != "cut off" {
		t.Fatalf("isolated batch missing the alert: %+v", p.NodeAlert)
	}
	s.Broadcast(context.Background(), raftcluster.MetricBatchPayload{HostMAC: "x"})
	time.Sleep(150 * time.Millisecond)
	if got := posts.Load(); got != 3 {
		t.Fatalf("throttled tick shipped anyway: posts = %d", got)
	}
	// Past the interval the next tick ships again (still with the alert).
	s.lastShipAt.Store(time.Now().Add(-isolatedShipInterval - time.Second).UnixNano())
	s.Broadcast(context.Background(), raftcluster.MetricBatchPayload{HostMAC: "x"})
	waitPosts(4)
	if p := <-bodies; p.NodeAlert == nil {
		t.Fatal("re-shipped isolated batch lost the alert")
	}

	// Recovered: full rate again, no alert on the wire.
	alert.Store(nil)
	s.Broadcast(context.Background(), raftcluster.MetricBatchPayload{HostMAC: "x"})
	s.Broadcast(context.Background(), raftcluster.MetricBatchPayload{HostMAC: "x"})
	waitPosts(6)
	for i := 0; i < 2; i++ {
		if p := <-bodies; p.NodeAlert != nil {
			t.Fatalf("recovered batch still carried an alert: %+v", p.NodeAlert)
		}
	}
}
