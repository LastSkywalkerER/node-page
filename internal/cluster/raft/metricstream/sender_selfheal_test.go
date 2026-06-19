package metricstream

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/charmbracelet/log"

	"system-stats/internal/app/config"
	raftcluster "system-stats/internal/cluster/raft"
)

// TestSenderSetBridgeSelfHeals proves the fix for the "topology replicates but
// metrics show -- on the hub after a live uplink (re)configuration" bug: a
// sender built with no hub seeds ships nothing cross-cluster until SetBridge
// re-points it at the hub, after which Broadcast delivers there — no restart.
func TestSenderSetBridgeSelfHeals(t *testing.T) {
	got := make(chan string, 4)
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got <- r.Header.Get("X-Bridge-Cluster-ID")
		w.WriteHeader(http.StatusOK)
	}))
	defer hub.Close()

	// intraSecret "" skips the same-cluster path (no DB needed); bridge unset.
	s := NewSender(log.New(io.Discard), nil, "spoke", "n1", "", config.RaftBridgeConfig{})

	// Before SetBridge: no hub configured → nothing ships cross-cluster.
	s.Broadcast(context.Background(), raftcluster.MetricBatchPayload{HostMAC: "x"})
	select {
	case <-got:
		t.Fatal("metrics shipped to the hub before any uplink was configured")
	case <-time.After(150 * time.Millisecond):
	}

	// Live (re)configure the uplink, mirroring ConfigureBridge → buildBridgeLocked.
	s.SetBridge(config.RaftBridgeConfig{
		Mode:         config.BridgeModePush,
		SharedSecret: "shared-hmac",
		RemoteSeeds:  []string{hub.URL},
	})

	s.Broadcast(context.Background(), raftcluster.MetricBatchPayload{HostMAC: "x"})
	select {
	case sender := <-got:
		if sender != "spoke" {
			t.Errorf("sender cluster header = %q, want spoke", sender)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("metrics did not reach the hub after SetBridge re-pointed the uplink")
	}
}
