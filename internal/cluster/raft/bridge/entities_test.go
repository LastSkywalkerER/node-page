package bridge

import (
	"context"
	"sync"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	// A ":memory:" SQLite DB is connection-scoped; pin the pool to a single
	// connection so concurrent goroutines (the redelivery race test) share the
	// one DB that actually holds the migrated table.
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("db handle: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	if err := AutoMigrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func TestAppliedIndexSetBatchDedup(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	// Seed indexes 1,2,3 for cluster "A" and 5 for cluster "B".
	if err := MarkAppliedBatch(ctx, db, []AppliedRemoteLog{
		{OriginClusterID: "A", OriginIndex: 1},
		{OriginClusterID: "A", OriginIndex: 2},
		{OriginClusterID: "A", OriginIndex: 3},
		{OriginClusterID: "B", OriginIndex: 5},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Query a mixed batch for cluster A: 2,3 already applied; 4,7 fresh.
	set, err := AppliedIndexSet(ctx, db, "A", []uint64{2, 3, 4, 7})
	if err != nil {
		t.Fatalf("AppliedIndexSet: %v", err)
	}
	if _, ok := set[2]; !ok {
		t.Errorf("index 2 should be in applied set")
	}
	if _, ok := set[3]; !ok {
		t.Errorf("index 3 should be in applied set")
	}
	if _, ok := set[4]; ok {
		t.Errorf("index 4 should NOT be applied")
	}
	if _, ok := set[7]; ok {
		t.Errorf("index 7 should NOT be applied")
	}
	if len(set) != 2 {
		t.Errorf("expected exactly 2 applied indexes, got %d", len(set))
	}

	// Index 5 belongs to cluster B, so it must NOT match under origin A.
	setA5, err := AppliedIndexSet(ctx, db, "A", []uint64{5})
	if err != nil {
		t.Fatalf("AppliedIndexSet A/5: %v", err)
	}
	if len(setA5) != 0 {
		t.Errorf("index 5 belongs to cluster B; must not match under A, got %v", setA5)
	}
}

func TestAppliedIndexSetEmptyIsNoop(t *testing.T) {
	db := newTestDB(t)
	set, err := AppliedIndexSet(context.Background(), db, "A", nil)
	if err != nil {
		t.Fatalf("empty AppliedIndexSet: %v", err)
	}
	if len(set) != 0 {
		t.Errorf("expected empty set, got %v", set)
	}
}

func TestMarkAppliedOnConflictIdempotent(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	// First insert.
	if err := MarkApplied(ctx, db, "A", "node-1", 42); err != nil {
		t.Fatalf("first MarkApplied: %v", err)
	}
	// Duplicate insert (a retried delivery) must be swallowed, NOT error.
	if err := MarkApplied(ctx, db, "A", "node-1", 42); err != nil {
		t.Fatalf("duplicate MarkApplied should be a no-op, got %v", err)
	}

	var count int64
	if err := db.Model(&AppliedRemoteLog{}).
		Where("origin_cluster_id = ? AND origin_index = ?", "A", uint64(42)).
		Count(&count).Error; err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Errorf("expected exactly 1 row after duplicate insert, got %d", count)
	}
}

// TestMarkAppliedConcurrentRedelivery exercises the race the OnConflict change
// fixes: concurrent redelivery of the same (origin, index) must never produce a
// duplicate-primary-key error. (SQLite serialises writes, but the OnConflict
// clause is what makes each call a clean no-op rather than a pkey violation.)
func TestMarkAppliedConcurrentRedelivery(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	const goroutines = 16
	var wg sync.WaitGroup
	errs := make(chan error, goroutines)
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := MarkApplied(ctx, db, "A", "node-1", 99); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent MarkApplied errored: %v", err)
	}

	var count int64
	if err := db.Model(&AppliedRemoteLog{}).
		Where("origin_cluster_id = ? AND origin_index = ?", "A", uint64(99)).
		Count(&count).Error; err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Errorf("expected exactly 1 row after concurrent redelivery, got %d", count)
	}
}

func TestMarkAppliedBatchEmptyIsNoop(t *testing.T) {
	db := newTestDB(t)
	if err := MarkAppliedBatch(context.Background(), db, nil); err != nil {
		t.Fatalf("empty MarkAppliedBatch should be a no-op, got %v", err)
	}
}

func TestNextBackoffProgression(t *testing.T) {
	t.Parallel()
	// First step seeds at backoffBase regardless of jitter sign.
	first := nextBackoff(0)
	if first <= 0 {
		t.Fatalf("first backoff must be positive, got %v", first)
	}
	if first > backoffBase+backoffBase/2 {
		t.Errorf("first backoff %v exceeds backoffBase+jitter ceiling", first)
	}
	// Repeated escalation must stay capped (cap + 25% jitter ceiling).
	cur := backoffBase
	ceiling := backoffMax + backoffMax/4
	for i := 0; i < 20; i++ {
		cur = nextBackoff(cur)
		if cur < 0 {
			t.Fatalf("backoff went negative: %v", cur)
		}
		if cur > ceiling {
			t.Fatalf("backoff %v exceeded cap+jitter ceiling %v at step %d", cur, ceiling, i)
		}
	}
}
