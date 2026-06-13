package database

import (
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"

	cpu "system-stats/internal/metrics/cpu"
	docker "system-stats/internal/metrics/docker"
)

func openMem(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared&_pragma=foreign_keys(on)"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

// pragmaPKCols reports how many columns make up the table's primary key.
func pragmaPKCols(t *testing.T, db *gorm.DB, table string) int {
	t.Helper()
	var rows []sqliteColumnInfo
	if err := db.Raw("PRAGMA table_info(" + table + ")").Scan(&rows).Error; err != nil {
		t.Fatalf("pragma %s: %v", table, err)
	}
	n := 0
	for _, r := range rows {
		if r.PK > 0 {
			n++
		}
	}
	return n
}

func fkCount(t *testing.T, db *gorm.DB, table string) int {
	t.Helper()
	type fk struct {
		ID int `gorm:"column:id"`
	}
	var rows []fk
	if err := db.Raw("PRAGMA foreign_key_list(" + table + ")").Scan(&rows).Error; err != nil {
		t.Fatalf("pragma foreign_key_list %s: %v", table, err)
	}
	return len(rows)
}

// TestMigrateFreshSQLiteCompositePK proves a fresh DB gets composite PKs and
// that running Migrate twice is a no-op.
func TestMigrateFreshSQLiteCompositePK(t *testing.T) {
	db := openMem(t)

	if err := Migrate(db); err != nil {
		t.Fatalf("first Migrate: %v", err)
	}

	metricTables := []string{"cpu_metrics", "memory_metrics", "disk_metrics", "network_metrics", "docker_metrics"}
	for _, table := range metricTables {
		if got := pragmaPKCols(t, db, table); got != 2 {
			t.Fatalf("%s: expected composite (2-col) PK, got %d cols", table, got)
		}
	}

	// No FK from docker_container_entities anymore.
	if got := fkCount(t, db, "docker_container_entities"); got != 0 {
		t.Fatalf("docker_container_entities: expected 0 FKs, got %d", got)
	}

	// Idempotent: a second full migration must not error or change the PK shape.
	if err := Migrate(db); err != nil {
		t.Fatalf("second Migrate (idempotency): %v", err)
	}
	for _, table := range metricTables {
		if got := pragmaPKCols(t, db, table); got != 2 {
			t.Fatalf("%s after 2nd migrate: expected 2-col PK, got %d", table, got)
		}
	}
}

// TestMigrateOldSchemaRebuild seeds a DB with the OLD single-column-PK schema
// (plus the legacy FK) and proves the rebuild produces a composite PK, preserves
// data, backfills NULL host_id, and is idempotent.
func TestMigrateOldSchemaRebuild(t *testing.T) {
	db := openMem(t)

	// --- Recreate the OLD schema by hand (single-column timestamp PK). ---
	if err := db.Exec(`CREATE TABLE cpu_metrics (
		host_id INTEGER,
		timestamp DATETIME PRIMARY KEY,
		usage REAL, cores INTEGER,
		load_avg_1 REAL, load_avg_5 REAL, load_avg_15 REAL, temperature REAL
	)`).Error; err != nil {
		t.Fatalf("create old cpu_metrics: %v", err)
	}
	if err := db.Exec(`CREATE TABLE memory_metrics (
		host_id INTEGER, timestamp DATETIME PRIMARY KEY,
		usage_percent REAL, used_bytes INTEGER, total_bytes INTEGER
	)`).Error; err != nil {
		t.Fatalf("create old memory_metrics: %v", err)
	}
	if err := db.Exec(`CREATE TABLE disk_metrics (
		host_id INTEGER, timestamp DATETIME PRIMARY KEY,
		usage_percent REAL, used_bytes INTEGER, total_bytes INTEGER
	)`).Error; err != nil {
		t.Fatalf("create old disk_metrics: %v", err)
	}
	if err := db.Exec(`CREATE TABLE network_metrics (
		host_id INTEGER, timestamp DATETIME PRIMARY KEY,
		interfaces TEXT
	)`).Error; err != nil {
		t.Fatalf("create old network_metrics: %v", err)
	}
	if err := db.Exec(`CREATE TABLE docker_metrics (
		host_id INTEGER, timestamp DATETIME PRIMARY KEY,
		total_containers INTEGER, running_containers INTEGER, docker_available NUMERIC
	)`).Error; err != nil {
		t.Fatalf("create old docker_metrics: %v", err)
	}
	// Old docker_container_entities WITH the legacy FK to docker_metrics(timestamp).
	if err := db.Exec(`CREATE TABLE docker_container_entities (
		id TEXT,
		metric_timestamp DATETIME,
		name TEXT,
		PRIMARY KEY (id, metric_timestamp),
		CONSTRAINT fk_docker_metrics_containers FOREIGN KEY (metric_timestamp) REFERENCES docker_metrics(timestamp)
	)`).Error; err != nil {
		t.Fatalf("create old docker_container_entities: %v", err)
	}

	// Seed data: one row with a real host_id, one with NULL host_id (to verify
	// the backfill to host 1).
	ts1 := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)
	ts2 := time.Date(2026, 6, 1, 10, 0, 5, 0, time.UTC)
	if err := db.Exec(`INSERT INTO cpu_metrics (host_id, timestamp, usage) VALUES (?, ?, ?)`, 2, ts1, 42.0).Error; err != nil {
		t.Fatalf("seed cpu row 1: %v", err)
	}
	if err := db.Exec(`INSERT INTO cpu_metrics (host_id, timestamp, usage) VALUES (NULL, ?, ?)`, ts2, 13.0).Error; err != nil {
		t.Fatalf("seed cpu row 2 (null host): %v", err)
	}
	if err := db.Exec(`INSERT INTO docker_metrics (host_id, timestamp, total_containers) VALUES (?, ?, ?)`, 1, ts1, 3).Error; err != nil {
		t.Fatalf("seed docker_metrics: %v", err)
	}
	if err := db.Exec(`INSERT INTO docker_container_entities (id, metric_timestamp, name) VALUES (?, ?, ?)`, "abc", ts1, "web").Error; err != nil {
		t.Fatalf("seed container: %v", err)
	}

	// Sanity: the FK exists pre-migration.
	if got := fkCount(t, db, "docker_container_entities"); got == 0 {
		t.Fatalf("precondition: expected FK on docker_container_entities, got 0")
	}

	// --- Run the migration. ---
	if err := Migrate(db); err != nil {
		t.Fatalf("Migrate over old schema: %v", err)
	}

	metricTables := []string{"cpu_metrics", "memory_metrics", "disk_metrics", "network_metrics", "docker_metrics"}
	for _, table := range metricTables {
		if got := pragmaPKCols(t, db, table); got != 2 {
			t.Fatalf("%s: expected 2-col PK after rebuild, got %d", table, got)
		}
	}
	if got := fkCount(t, db, "docker_container_entities"); got != 0 {
		t.Fatalf("docker_container_entities: expected FK dropped, got %d", got)
	}

	// Data preserved + NULL host_id backfilled to 1.
	var cpuRows []cpu.HistoricalCPUMetric
	if err := db.Order("timestamp ASC").Find(&cpuRows).Error; err != nil {
		t.Fatalf("read cpu rows: %v", err)
	}
	if len(cpuRows) != 2 {
		t.Fatalf("expected 2 cpu rows preserved, got %d", len(cpuRows))
	}
	if cpuRows[0].HostID == nil || *cpuRows[0].HostID != 2 {
		t.Fatalf("row 1 host_id: expected 2, got %v", cpuRows[0].HostID)
	}
	if cpuRows[1].HostID == nil || *cpuRows[1].HostID != 1 {
		t.Fatalf("row 2 host_id: expected backfilled 1, got %v", cpuRows[1].HostID)
	}

	// docker_container_entities is now a current-state table: the legacy
	// (id, metric_timestamp) table is DROPPED and recreated empty in the new
	// shape (its rows are ephemeral, rebuilt on the next collection tick). So the
	// seeded row is gone, and the rebuilt table carries a host_id column.
	var containerCount int64
	if err := db.Table("docker_container_entities").Count(&containerCount).Error; err != nil {
		t.Fatalf("count containers: %v", err)
	}
	if containerCount != 0 {
		t.Fatalf("expected legacy container rows dropped (current-state rebuild), got %d", containerCount)
	}
	if !db.Migrator().HasColumn(&docker.DockerContainerEntity{}, "host_id") {
		t.Fatalf("rebuilt docker_container_entities should have a host_id column")
	}

	// Idempotent: a second migration over the now-rebuilt schema is a no-op.
	if err := Migrate(db); err != nil {
		t.Fatalf("second Migrate (idempotency over rebuilt schema): %v", err)
	}
	for _, table := range metricTables {
		if got := pragmaPKCols(t, db, table); got != 2 {
			t.Fatalf("%s after 2nd migrate: expected 2-col PK, got %d", table, got)
		}
	}
}

// TestOnConflictTargetsCompositePK confirms clause.OnConflict{DoNothing:true}
// without explicit columns targets the composite (host_id, timestamp) PK: a
// second insert of the SAME (host_id, timestamp) is ignored, but a DIFFERENT
// host_id at the SAME timestamp is a distinct row (which the old timestamp-only
// PK would have rejected).
func TestOnConflictTargetsCompositePK(t *testing.T) {
	db := openMem(t)
	if err := Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	ts := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	h1 := uint(1)
	h2 := uint(2)

	mk := func(h uint, usage float64) *cpu.HistoricalCPUMetric {
		return &cpu.HistoricalCPUMetric{HostID: &h, Timestamp: ts, Usage: usage}
	}

	// First insert (host 1).
	if err := db.Clauses(clause.OnConflict{DoNothing: true}).Create(mk(h1, 10)).Error; err != nil {
		t.Fatalf("insert h1: %v", err)
	}
	// Same (host 1, ts) again — must be a silent no-op, not an error.
	if err := db.Clauses(clause.OnConflict{DoNothing: true}).Create(mk(h1, 99)).Error; err != nil {
		t.Fatalf("re-insert h1 (should be no-op): %v", err)
	}
	// Different host, SAME timestamp — must be a NEW row (composite PK).
	if err := db.Clauses(clause.OnConflict{DoNothing: true}).Create(mk(h2, 20)).Error; err != nil {
		t.Fatalf("insert h2 same ts: %v", err)
	}

	var rows []cpu.HistoricalCPUMetric
	if err := db.Order("host_id ASC").Find(&rows).Error; err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows (h1 + h2 at same ts), got %d", len(rows))
	}
	// host 1's row kept its original usage (DoNothing didn't overwrite).
	if rows[0].Usage != 10 {
		t.Fatalf("host 1 usage: expected 10 (unchanged), got %v", rows[0].Usage)
	}
}
