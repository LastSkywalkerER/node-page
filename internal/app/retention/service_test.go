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

// TestCleanupBatchDockerContainers proves retention now prunes
// docker_container_entities by metric_timestamp (it was previously uncovered),
// deleting only rows older than the retention cutoff.
func TestCleanupBatchDockerContainers(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	// Minimal schema: the cleanup runs raw DELETEs against these table names.
	if err := db.Exec(`CREATE TABLE docker_metrics (host_id INTEGER, timestamp DATETIME, PRIMARY KEY (host_id, timestamp))`).Error; err != nil {
		t.Fatalf("create docker_metrics: %v", err)
	}
	if err := db.Exec(`CREATE TABLE cpu_metrics (host_id INTEGER, timestamp DATETIME, PRIMARY KEY (host_id, timestamp))`).Error; err != nil {
		t.Fatalf("create cpu_metrics: %v", err)
	}
	for _, tbl := range []string{"memory_metrics", "disk_metrics", "network_metrics"} {
		if err := db.Exec("CREATE TABLE " + tbl + " (host_id INTEGER, timestamp DATETIME, PRIMARY KEY (host_id, timestamp))").Error; err != nil {
			t.Fatalf("create %s: %v", tbl, err)
		}
	}
	if err := db.Exec(`CREATE TABLE docker_container_entities (id TEXT, metric_timestamp DATETIME, PRIMARY KEY (id, metric_timestamp))`).Error; err != nil {
		t.Fatalf("create docker_container_entities: %v", err)
	}

	old := time.Now().AddDate(0, 0, -40) // older than 30d retention
	fresh := time.Now().AddDate(0, 0, -1)
	if err := db.Exec(`INSERT INTO docker_container_entities (id, metric_timestamp) VALUES ('old', ?), ('fresh', ?)`, old, fresh).Error; err != nil {
		t.Fatalf("seed containers: %v", err)
	}

	svc := NewService(db, log.New(io.Discard), 30)
	if _, err := svc.CleanupBatch(context.Background(), 100); err != nil {
		t.Fatalf("cleanup: %v", err)
	}

	var ids []string
	if err := db.Raw(`SELECT id FROM docker_container_entities ORDER BY id`).Scan(&ids).Error; err != nil {
		t.Fatalf("read remaining: %v", err)
	}
	if len(ids) != 1 || ids[0] != "fresh" {
		t.Fatalf("expected only 'fresh' container to remain, got %v", ids)
	}
}
