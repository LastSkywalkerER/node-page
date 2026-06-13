package retention

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/charmbracelet/log"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// TestCleanupBatchPrunesMetricsNotContainers proves retention prunes the metric
// time-series tables (here docker_metrics) by the time cutoff, but leaves
// docker_container_entities alone — it is now a current-state table (one row per
// live container, pruned on write), not a time series, so retention must not
// touch it regardless of how old metric_timestamp is.
func TestCleanupBatchPrunesMetricsNotContainers(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.Exec(`CREATE TABLE docker_metrics (host_id INTEGER, timestamp DATETIME, PRIMARY KEY (host_id, timestamp))`).Error; err != nil {
		t.Fatalf("create docker_metrics: %v", err)
	}
	for _, tbl := range []string{"cpu_metrics", "memory_metrics", "disk_metrics", "network_metrics"} {
		if err := db.Exec("CREATE TABLE " + tbl + " (host_id INTEGER, timestamp DATETIME, PRIMARY KEY (host_id, timestamp))").Error; err != nil {
			t.Fatalf("create %s: %v", tbl, err)
		}
	}
	if err := db.Exec(`CREATE TABLE docker_container_entities (id TEXT PRIMARY KEY, host_id INTEGER, metric_timestamp DATETIME)`).Error; err != nil {
		t.Fatalf("create docker_container_entities: %v", err)
	}

	old := time.Now().AddDate(0, 0, -40) // older than 30d retention
	fresh := time.Now().AddDate(0, 0, -1)
	if err := db.Exec(`INSERT INTO docker_metrics (host_id, timestamp) VALUES (1, ?), (1, ?)`, old, fresh).Error; err != nil {
		t.Fatalf("seed docker_metrics: %v", err)
	}
	// A current-state container whose last-written timestamp is ancient — it is
	// still live, so retention must keep it.
	if err := db.Exec(`INSERT INTO docker_container_entities (id, host_id, metric_timestamp) VALUES ('live', 1, ?)`, old).Error; err != nil {
		t.Fatalf("seed container: %v", err)
	}

	svc := NewService(db, log.New(io.Discard), 30)
	if _, err := svc.CleanupBatch(context.Background(), 100); err != nil {
		t.Fatalf("cleanup: %v", err)
	}

	var metricRows int64
	if err := db.Raw(`SELECT count(*) FROM docker_metrics`).Scan(&metricRows).Error; err != nil {
		t.Fatalf("count docker_metrics: %v", err)
	}
	if metricRows != 1 {
		t.Fatalf("expected only the fresh docker_metrics row to remain, got %d", metricRows)
	}

	var containerRows int64
	if err := db.Raw(`SELECT count(*) FROM docker_container_entities`).Scan(&containerRows).Error; err != nil {
		t.Fatalf("count containers: %v", err)
	}
	if containerRows != 1 {
		t.Fatalf("retention must not touch current-state containers, got %d rows", containerRows)
	}
}
