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

	users "system-stats/internal/auth/users"
	appicons "system-stats/internal/platform/appicons"
)

// TestCleanupExpiredIcons prunes app_icon_cache rows past expiry, keeping fresh ones.
func TestCleanupExpiredIcons(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&appicons.IconCacheEntry{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	db.Create(&appicons.IconCacheEntry{Key: "stale", OK: true, ExpiresAt: time.Now().Add(-time.Hour)})
	db.Create(&appicons.IconCacheEntry{Key: "fresh", OK: true, ExpiresAt: time.Now().Add(time.Hour)})

	svc := NewService(db, log.New(io.Discard), 30, nil)
	svc.CleanupExpiredIcons(context.Background())

	var keys []string
	db.Model(&appicons.IconCacheEntry{}).Pluck("key", &keys)
	if len(keys) != 1 || keys[0] != "fresh" {
		t.Fatalf("want only [fresh], got %v", keys)
	}
}

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

	svc := NewService(db, log.New(io.Discard), 30, nil)
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

// TestCleanupExpiredTokensPrunesExpiredAndLongRevoked proves the refresh-token
// cleanup deletes tokens that are past expiry OR revoked beyond the retention
// window, while keeping active tokens and freshly-revoked ones (still inside the
// rotation grace).
func TestCleanupExpiredTokensPrunesExpiredAndLongRevoked(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&users.RefreshToken{}); err != nil {
		t.Fatalf("migrate refresh tokens: %v", err)
	}

	now := time.Now()
	insert := func(jti string, expiresAt time.Time, revokedAt *time.Time) {
		if err := db.Exec(
			`INSERT INTO user_refresh_tokens (user_id, jti, token_hash, expires_at, revoked_at, created_at) VALUES (1, ?, ?, ?, ?, ?)`,
			jti, "hash-"+jti, expiresAt, revokedAt, now,
		).Error; err != nil {
			t.Fatalf("insert %s: %v", jti, err)
		}
	}
	twoHrsAgo := now.Add(-2 * time.Hour)
	thirtySecAgo := now.Add(-30 * time.Second)
	insert("expired", now.Add(-time.Hour), nil)                  // past expiry -> delete
	insert("long-revoked", now.Add(72*time.Hour), &twoHrsAgo)    // revoked >1h ago -> delete
	insert("active", now.Add(72*time.Hour), nil)                 // live -> keep
	insert("just-revoked", now.Add(72*time.Hour), &thirtySecAgo) // within grace -> keep

	svc := NewService(db, log.New(io.Discard), 30, users.NewRefreshTokenRepository(db))
	svc.CleanupExpiredTokens(context.Background())

	var remaining []string
	if err := db.Raw(`SELECT jti FROM user_refresh_tokens ORDER BY jti`).Scan(&remaining).Error; err != nil {
		t.Fatalf("scan remaining: %v", err)
	}
	if len(remaining) != 2 || remaining[0] != "active" || remaining[1] != "just-revoked" {
		t.Fatalf("expected [active just-revoked] to remain, got %v", remaining)
	}

	// Second immediate call is throttled — a no-op, never an error/panic.
	svc.CleanupExpiredTokens(context.Background())
}

// newOrphanTestDB builds the metric tables plus a hosts table with one live host.
func newOrphanTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.Exec(`CREATE TABLE hosts (id INTEGER PRIMARY KEY, name TEXT)`).Error; err != nil {
		t.Fatalf("create hosts: %v", err)
	}
	for _, tbl := range MetricTables {
		if err := db.Exec("CREATE TABLE " + tbl + " (host_id INTEGER, timestamp DATETIME, PRIMARY KEY (host_id, timestamp))").Error; err != nil {
			t.Fatalf("create %s: %v", tbl, err)
		}
	}
	if err := db.Exec(`CREATE TABLE docker_container_entities (id TEXT PRIMARY KEY, host_id INTEGER, metric_timestamp DATETIME)`).Error; err != nil {
		t.Fatalf("create docker_container_entities: %v", err)
	}
	return db
}

// History belonging to a host that no longer exists is collected. This is where
// the rows land when a replicated host delete drops the row and runs out of
// budget before draining the (possibly million-row) history behind it.
func TestCleanupOrphanMetricsCollectsDeletedHostsHistory(t *testing.T) {
	db := newOrphanTestDB(t)
	now := time.Now()
	if err := db.Exec(`INSERT INTO hosts (id, name) VALUES (1, 'live')`).Error; err != nil {
		t.Fatalf("seed hosts: %v", err)
	}
	for _, tbl := range MetricTables {
		if err := db.Exec("INSERT INTO "+tbl+" (host_id, timestamp) VALUES (1, ?), (7, ?)", now, now).Error; err != nil {
			t.Fatalf("seed %s: %v", tbl, err)
		}
	}
	if err := db.Exec(`INSERT INTO docker_container_entities (id, host_id, metric_timestamp) VALUES ('live', 1, ?), ('ghost', 7, ?)`, now, now).Error; err != nil {
		t.Fatalf("seed containers: %v", err)
	}

	NewService(db, log.New(io.Discard), 30, nil).CleanupOrphanMetrics(context.Background())

	for _, tbl := range append(append([]string{}, MetricTables...), "docker_container_entities") {
		var orphans, live int64
		if err := db.Raw("SELECT count(*) FROM " + tbl + " WHERE host_id = 7").Scan(&orphans).Error; err != nil {
			t.Fatalf("count orphans in %s: %v", tbl, err)
		}
		if orphans != 0 {
			t.Errorf("%s: %d orphan rows left", tbl, orphans)
		}
		if err := db.Raw("SELECT count(*) FROM " + tbl + " WHERE host_id = 1").Scan(&live).Error; err != nil {
			t.Fatalf("count live in %s: %v", tbl, err)
		}
		if live != 1 {
			t.Errorf("%s: live host's rows were touched (%d left, want 1)", tbl, live)
		}
	}
}

// An empty hosts table reads as a half-migrated or mid-restore database, not as
// a node that legitimately knows no machines — it must never trigger a wipe.
func TestCleanupOrphanMetricsKeepsEverythingWhenNoHostsAreKnown(t *testing.T) {
	db := newOrphanTestDB(t)
	now := time.Now()
	if err := db.Exec(`INSERT INTO cpu_metrics (host_id, timestamp) VALUES (3, ?)`, now).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}

	NewService(db, log.New(io.Discard), 30, nil).CleanupOrphanMetrics(context.Background())

	var rows int64
	if err := db.Raw(`SELECT count(*) FROM cpu_metrics`).Scan(&rows).Error; err != nil {
		t.Fatalf("count: %v", err)
	}
	if rows != 1 {
		t.Fatalf("metric rows were purged with an empty hosts table, %d left", rows)
	}
}

// The sweep is throttled: the driving hook fires every few seconds and must not
// re-scan every table each time.
func TestCleanupOrphanMetricsIsThrottled(t *testing.T) {
	db := newOrphanTestDB(t)
	now := time.Now()
	if err := db.Exec(`INSERT INTO hosts (id, name) VALUES (1, 'live')`).Error; err != nil {
		t.Fatalf("seed hosts: %v", err)
	}
	svc := NewService(db, log.New(io.Discard), 30, nil)
	svc.CleanupOrphanMetrics(context.Background())

	if err := db.Exec(`INSERT INTO cpu_metrics (host_id, timestamp) VALUES (9, ?)`, now).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}
	svc.CleanupOrphanMetrics(context.Background()) // too soon — must be a no-op

	var rows int64
	if err := db.Raw(`SELECT count(*) FROM cpu_metrics WHERE host_id = 9`).Scan(&rows).Error; err != nil {
		t.Fatalf("count: %v", err)
	}
	if rows != 1 {
		t.Fatalf("the throttle did not hold the second sweep back")
	}
}
