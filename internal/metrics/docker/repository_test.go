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

func sampleMetric() DockerMetric {
	return DockerMetric{
		TotalContainers:   1,
		RunningContainers: 1,
		DockerAvailable:   true,
		Stacks: []DockerStack{{
			Name:              "app",
			TotalContainers:   1,
			RunningContainers: 1,
			Containers: []DockerContainer{{
				ID:    "container-a",
				Name:  "app-web-1",
				Image: "nginx:1.25",
				State: "running",
			}},
		}},
	}
}

// TestDockerSaveCompositePKAndPreload proves the composite-PK insert path works:
// the metric + its container persist, the Containers association still preloads
// without a DB-level FK, replay of the same (host_id, timestamp) is a silent
// no-op (OnConflict{DoNothing} targets the composite PK), and a different host
// at the SAME timestamp is a distinct row.
func TestDockerSaveCompositePKAndPreload(t *testing.T) {
	repo, db := newDockerTestRepo(t)
	ctx := context.Background()
	ts := time.Date(2026, 6, 1, 9, 0, 0, 0, time.UTC)

	if err := repo.SaveCurrentMetricAt(ctx, sampleMetric(), 1, ts); err != nil {
		t.Fatalf("save host 1: %v", err)
	}

	// Replay: identical (host_id, timestamp) must be a no-op, not a PK violation.
	if err := repo.SaveCurrentMetricAt(ctx, sampleMetric(), 1, ts); err != nil {
		t.Fatalf("replay host 1 (should be no-op): %v", err)
	}

	// Different host, SAME timestamp — distinct row under the composite PK.
	if err := repo.SaveCurrentMetricAt(ctx, sampleMetric(), 2, ts); err != nil {
		t.Fatalf("save host 2 same ts: %v", err)
	}

	var metricCount int64
	if err := db.Model(&HistoricalDockerMetric{}).Count(&metricCount).Error; err != nil {
		t.Fatalf("count metrics: %v", err)
	}
	if metricCount != 2 {
		t.Fatalf("expected 2 docker_metrics rows (host1 + host2 same ts), got %d", metricCount)
	}

	// Preload still resolves the container association by MetricTimestamp even
	// though no DB foreign key exists.
	got, err := repo.GetLatestMetricByHost(ctx, 1)
	if err != nil {
		t.Fatalf("get latest host 1: %v", err)
	}
	if got == nil || len(got.Stacks) == 0 || len(got.Stacks[0].Containers) != 1 {
		t.Fatalf("expected 1 preloaded container for host 1, got %+v", got)
	}
	if got.Stacks[0].Containers[0].ID != "container-a" {
		t.Fatalf("unexpected container id: %q", got.Stacks[0].Containers[0].ID)
	}
}
