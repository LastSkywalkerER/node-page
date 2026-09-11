package raft

import (
	"context"
	"testing"
	"time"

	"gorm.io/gorm"

	hosts "system-stats/internal/cluster/hosts"
)

// alertSinkRepo is the minimal hosts.Repository the sink touches for a batch
// that carries no metrics: MAC resolution + the liveness bump.
type alertSinkRepo struct {
	hosts.Repository
	byMAC map[string]*hosts.Host
}

func (r *alertSinkRepo) GetHostByMacAddress(_ context.Context, mac string) (*hosts.Host, error) {
	if h, ok := r.byMAC[mac]; ok {
		return h, nil
	}
	return nil, gorm.ErrRecordNotFound
}

func (r *alertSinkRepo) UpdateLastSeenAndAgentSession(context.Context, uint, time.Time, *time.Time) error {
	return nil
}

// TestMetricSinkStoresAndClearsNodeAlert: a batch carrying the sender's
// self-diagnosis lands in the alert store under the LOCAL host id; the next
// batch without one clears it immediately (no waiting on the TTL); the local
// collector's own row never takes an alert from the wire.
func TestMetricSinkStoresAndClearsNodeAlert(t *testing.T) {
	t.Parallel()
	store := hosts.NewNodeAlertStore()
	repo := &alertSinkRepo{byMAC: map[string]*hosts.Host{
		"02:42:ac:1b:00:03": {ID: 9, Name: "SkyNAS", MacAddress: "02:42:ac:1b:00:03"},
		"02:42:0a:00:0c:03": {ID: hosts.LocalCollectorHostID, MacAddress: "02:42:0a:00:0c:03"},
	}}
	s := &MetricSink{hostRepo: repo, alerts: store}

	alert := &hosts.NodeAlert{Kind: hosts.NodeAlertRaftIsolated, Title: "cut off", NodeID: "skynas"}
	if err := s.Ingest(context.Background(), MetricBatchPayload{HostMAC: "02:42:ac:1b:00:03", Timestamp: time.Now(), NodeAlert: alert}, ""); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if got := store.NodeAlert(9); got == nil || got.NodeID != "skynas" {
		t.Fatalf("alert not stored for host 9: %+v", got)
	}

	if err := s.Ingest(context.Background(), MetricBatchPayload{HostMAC: "02:42:ac:1b:00:03", Timestamp: time.Now()}, ""); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if got := store.NodeAlert(9); got != nil {
		t.Fatalf("alert not cleared by an alert-less batch: %+v", got)
	}

	// Our own machine's batch (id=1) is ignored by the sink, alert included.
	_ = s.Ingest(context.Background(), MetricBatchPayload{HostMAC: "02:42:0a:00:0c:03", Timestamp: time.Now(), NodeAlert: alert}, "")
	if got := store.NodeAlert(hosts.LocalCollectorHostID); got != nil {
		t.Fatalf("local row took an alert from the wire: %+v", got)
	}

	// Unknown host: transient no-op, nothing stored.
	if err := s.Ingest(context.Background(), MetricBatchPayload{HostMAC: "ff:ff:ff:ff:ff:ff", NodeAlert: alert}, ""); err != nil {
		t.Fatalf("unknown host should be a no-op, got %v", err)
	}
}
