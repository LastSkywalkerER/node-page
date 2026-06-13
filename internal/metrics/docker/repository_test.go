package docker

import (
	"context"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func newDockerTestRepo(t *testing.T) (DockerRepository, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file::memory:?_pragma=foreign_keys(on)"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&HistoricalDockerMetric{}, &DockerContainerEntity{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return NewRepository(db), db
}

// metricWith builds a DockerMetric carrying the given containers (id+state),
// available unless told otherwise.
func dockerMetricWith(available bool, containers ...DockerContainer) DockerMetric {
	running := 0
	for _, c := range containers {
		if c.State == "running" {
			running++
		}
	}
	m := DockerMetric{
		TotalContainers:   len(containers),
		RunningContainers: running,
		DockerAvailable:   available,
	}
	if len(containers) > 0 {
		m.Stacks = []DockerStack{{
			Name:              "app",
			TotalContainers:   len(containers),
			RunningContainers: running,
			Containers:        containers,
		}}
	}
	return m
}

func ctr(id, name, state string) DockerContainer {
	return DockerContainer{ID: id, Name: name, Image: "nginx:1.25", State: state}
}

// sampleMetric is a one-container available metric used by service_test.go.
func sampleMetric() DockerMetric {
	return dockerMetricWith(true, ctr("container-a", "app-web-1", "running"))
}

func countContainers(t *testing.T, db *gorm.DB, hostID uint) int64 {
	t.Helper()
	var n int64
	if err := db.Model(&DockerContainerEntity{}).Where("host_id = ?", hostID).Count(&n).Error; err != nil {
		t.Fatalf("count containers host %d: %v", hostID, err)
	}
	return n
}

// TestDockerCurrentStatePerHostIsolation: distinct containers per host coexist;
// each host's latest read returns only its own containers, and the counter rows
// are a per-(host_id, timestamp) time series.
func TestDockerCurrentStatePerHostIsolation(t *testing.T) {
	repo, db := newDockerTestRepo(t)
	ctx := context.Background()
	ts := time.Date(2026, 6, 1, 9, 0, 0, 0, time.UTC)

	if err := repo.SaveCurrentMetricAt(ctx, dockerMetricWith(true, ctr("c-a", "app-web-1", "running")), 1, ts); err != nil {
		t.Fatalf("save host 1: %v", err)
	}
	if err := repo.SaveCurrentMetricAt(ctx, dockerMetricWith(true, ctr("c-b", "api-1", "running")), 2, ts); err != nil {
		t.Fatalf("save host 2 same ts: %v", err)
	}

	var metricCount int64
	if err := db.Model(&HistoricalDockerMetric{}).Count(&metricCount).Error; err != nil {
		t.Fatalf("count metrics: %v", err)
	}
	if metricCount != 2 {
		t.Fatalf("expected 2 docker_metrics rows (host1 + host2 same ts), got %d", metricCount)
	}

	got1, err := repo.GetLatestMetricByHost(ctx, 1)
	if err != nil || got1 == nil || len(got1.Stacks) != 1 || len(got1.Stacks[0].Containers) != 1 || got1.Stacks[0].Containers[0].ID != "c-a" {
		t.Fatalf("host 1 latest: want [c-a], got %+v (err %v)", got1, err)
	}
	got2, err := repo.GetLatestMetricByHost(ctx, 2)
	if err != nil || got2 == nil || len(got2.Stacks) != 1 || len(got2.Stacks[0].Containers) != 1 || got2.Stacks[0].Containers[0].ID != "c-b" {
		t.Fatalf("host 2 latest: want [c-b], got %+v (err %v)", got2, err)
	}
}

// TestDockerSharedContainerIDAcrossHosts: two hosts that report the SAME
// container id (VMs cloned from a golden image carry identical baked-in ids)
// must keep DISTINCT rows — the composite (id, host_id) PK must not let one
// host's upsert overwrite/steal the other's row. Regression for the id-only PK
// flapping bug.
func TestDockerSharedContainerIDAcrossHosts(t *testing.T) {
	repo, db := newDockerTestRepo(t)
	ctx := context.Background()
	ts := time.Date(2026, 6, 1, 9, 0, 0, 0, time.UTC)

	if err := repo.SaveCurrentMetricAt(ctx, dockerMetricWith(true, ctr("shared", "web-1", "running")), 1, ts); err != nil {
		t.Fatalf("save host 1: %v", err)
	}
	if err := repo.SaveCurrentMetricAt(ctx, dockerMetricWith(true, ctr("shared", "web-1", "running")), 2, ts); err != nil {
		t.Fatalf("save host 2: %v", err)
	}
	// Re-save host 1 — must not be stolen back and forth.
	if err := repo.SaveCurrentMetricAt(ctx, dockerMetricWith(true, ctr("shared", "web-1", "running")), 1, ts.Add(5*time.Second)); err != nil {
		t.Fatalf("re-save host 1: %v", err)
	}

	if n := countContainers(t, db, 1); n != 1 {
		t.Fatalf("host 1 should still own its 'shared' row, got %d", n)
	}
	if n := countContainers(t, db, 2); n != 1 {
		t.Fatalf("host 2 should still own its 'shared' row, got %d", n)
	}
	var total int64
	db.Model(&DockerContainerEntity{}).Count(&total)
	if total != 2 {
		t.Fatalf("want 2 distinct rows for the shared id (one per host), got %d", total)
	}
}

// TestDockerCurrentStateUpsertAndPrune: a container changing state updates in
// place (no new row), a container that disappears is pruned, and the counter
// rows still accumulate as a time series. This is the core "write once / bounded
// table" guarantee — the table holds one row per LIVE container, not N per tick.
func TestDockerCurrentStateUpsertAndPrune(t *testing.T) {
	repo, db := newDockerTestRepo(t)
	ctx := context.Background()
	ts1 := time.Date(2026, 6, 1, 9, 0, 0, 0, time.UTC)
	ts2 := ts1.Add(5 * time.Second)

	if err := repo.SaveCurrentMetricAt(ctx, dockerMetricWith(true,
		ctr("c-a", "app-web-1", "running"),
		ctr("c-c", "app-cache-1", "running"),
	), 1, ts1); err != nil {
		t.Fatalf("save ts1: %v", err)
	}
	if n := countContainers(t, db, 1); n != 2 {
		t.Fatalf("after ts1 want 2 container rows, got %d", n)
	}

	// ts2: c-a stopped, c-c gone.
	if err := repo.SaveCurrentMetricAt(ctx, dockerMetricWith(true,
		ctr("c-a", "app-web-1", "exited"),
	), 1, ts2); err != nil {
		t.Fatalf("save ts2: %v", err)
	}
	if n := countContainers(t, db, 1); n != 1 {
		t.Fatalf("after ts2 want 1 container row (c-a updated, c-c pruned), got %d", n)
	}

	got, err := repo.GetLatestMetricByHost(ctx, 1)
	if err != nil || got == nil || len(got.Stacks) != 1 || len(got.Stacks[0].Containers) != 1 {
		t.Fatalf("latest after prune: %+v (err %v)", got, err)
	}
	if c := got.Stacks[0].Containers[0]; c.ID != "c-a" || c.State != "exited" {
		t.Fatalf("c-a should be updated in place to exited, got %+v", c)
	}

	// Counter rows are a time series: both ticks kept.
	var metricRows int64
	db.Model(&HistoricalDockerMetric{}).Where("host_id = ?", 1).Count(&metricRows)
	if metricRows != 2 {
		t.Fatalf("want 2 docker_metrics counter rows, got %d", metricRows)
	}
}

// TestDockerReplayNoOp: re-applying the identical (host_id, timestamp) batch —
// as Raft log replay does after a restart — is a silent no-op, not a PK
// violation, and does not duplicate rows.
func TestDockerReplayNoOp(t *testing.T) {
	repo, db := newDockerTestRepo(t)
	ctx := context.Background()
	ts := time.Date(2026, 6, 1, 9, 0, 0, 0, time.UTC)
	m := dockerMetricWith(true, ctr("c-a", "app-web-1", "running"))

	if err := repo.SaveCurrentMetricAt(ctx, m, 1, ts); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := repo.SaveCurrentMetricAt(ctx, m, 1, ts); err != nil {
		t.Fatalf("replay (should be no-op): %v", err)
	}
	var metricRows int64
	db.Model(&HistoricalDockerMetric{}).Count(&metricRows)
	if metricRows != 1 {
		t.Fatalf("replay duplicated the counter row: want 1, got %d", metricRows)
	}
	if n := countContainers(t, db, 1); n != 1 {
		t.Fatalf("replay duplicated the container row: want 1, got %d", n)
	}
}

// TestDockerUnavailableKeepsLastKnown: a transient Docker outage (no containers,
// DockerAvailable=false) must NOT wipe the last-known container list; only an
// authoritative empty set (available, zero containers) clears the host.
func TestDockerUnavailableKeepsLastKnown(t *testing.T) {
	repo, db := newDockerTestRepo(t)
	ctx := context.Background()
	base := time.Date(2026, 6, 1, 9, 0, 0, 0, time.UTC)

	if err := repo.SaveCurrentMetricAt(ctx, dockerMetricWith(true, ctr("c-a", "app-web-1", "running")), 1, base); err != nil {
		t.Fatalf("save available: %v", err)
	}
	// Outage: unavailable, no containers — keep last-known.
	if err := repo.SaveCurrentMetricAt(ctx, dockerMetricWith(false), 1, base.Add(5*time.Second)); err != nil {
		t.Fatalf("save unavailable: %v", err)
	}
	if n := countContainers(t, db, 1); n != 1 {
		t.Fatalf("outage should keep last-known container, got %d rows", n)
	}
	// Authoritative empty (available, zero containers) — clear the host.
	if err := repo.SaveCurrentMetricAt(ctx, dockerMetricWith(true), 1, base.Add(10*time.Second)); err != nil {
		t.Fatalf("save empty available: %v", err)
	}
	if n := countContainers(t, db, 1); n != 0 {
		t.Fatalf("authoritative empty set should clear host, got %d rows", n)
	}
}
