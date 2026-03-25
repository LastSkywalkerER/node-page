package retention_test

import (
	"testing"
	"time"

	"github.com/charmbracelet/log"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"system-stats/internal/app/retention"
)

func setupTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Discard,
	})
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	for _, table := range retention.MetricTables {
		sql := "CREATE TABLE " + table + " (id INTEGER PRIMARY KEY, host_id INTEGER, timestamp DATETIME)"
		if err := db.Exec(sql).Error; err != nil {
			t.Fatalf("create table %s: %v", table, err)
		}
	}
	return db
}

func insertRow(t *testing.T, db *gorm.DB, table string, ts time.Time) {
	t.Helper()
	if err := db.Exec("INSERT INTO "+table+" (timestamp) VALUES (?)", ts).Error; err != nil {
		t.Fatalf("insert into %s: %v", table, err)
	}
}

func countRows(t *testing.T, db *gorm.DB, table string) int64 {
	t.Helper()
	var n int64
	if err := db.Raw("SELECT COUNT(*) FROM " + table).Scan(&n).Error; err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

func TestCleanup_DeletesOldRows(t *testing.T) {
	db := setupTestDB(t)
	svc := retention.NewService(db, log.Default(), 30)

	old := time.Now().AddDate(0, 0, -60)
	insertRow(t, db, "cpu_metrics", old)
	insertRow(t, db, "cpu_metrics", old.Add(-time.Hour))

	svc.Cleanup()

	if n := countRows(t, db, "cpu_metrics"); n != 0 {
		t.Errorf("expected 0 rows after cleanup, got %d", n)
	}
}

func TestCleanup_PreservesRecentRows(t *testing.T) {
	db := setupTestDB(t)
	svc := retention.NewService(db, log.Default(), 30)

	recent := time.Now().Add(-time.Hour)
	insertRow(t, db, "cpu_metrics", recent)

	svc.Cleanup()

	if n := countRows(t, db, "cpu_metrics"); n != 1 {
		t.Errorf("expected 1 row preserved, got %d", n)
	}
}

func TestCleanup_MultipleTablesAtOnce(t *testing.T) {
	db := setupTestDB(t)
	svc := retention.NewService(db, log.Default(), 7)

	old := time.Now().AddDate(0, 0, -14)
	recent := time.Now().Add(-time.Hour)

	for _, table := range retention.MetricTables {
		insertRow(t, db, table, old)
		insertRow(t, db, table, recent)
	}

	svc.Cleanup()

	for _, table := range retention.MetricTables {
		n := countRows(t, db, table)
		if n != 1 {
			t.Errorf("%s: expected 1 row, got %d", table, n)
		}
	}
}
