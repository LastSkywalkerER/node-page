package gateway

import (
	"context"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func openTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file::memory:"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	return db
}

// Regression: the CIDR column used to be derived as "c_id_r" by GORM's naming
// strategy while the ON CONFLICT clause referenced excluded.cidr, so every
// insert failed ("no such column: excluded.cidr") and no block ever landed.
func TestBlockRepositoryUpsertRoundTrip(t *testing.T) {
	db := openTestDB(t)
	if err := AutoMigrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if !db.Migrator().HasColumn(&Block{}, "cidr") {
		t.Fatalf("gateway_blocks must have a column literally named cidr")
	}
	repo := NewBlockRepository(db)
	ctx := context.Background()

	exp := time.Now().UTC().Add(time.Hour).Truncate(time.Second)
	b := Block{BlockID: "abc123", CIDR: "203.0.113.7/32", Reason: "scanner", Source: BlockSourceManual, ExpiresAt: &exp}
	if err := repo.UpsertBlock(ctx, &b); err != nil {
		t.Fatalf("insert: %v", err)
	}
	rows, err := repo.ListBlocks(ctx)
	if err != nil || len(rows) != 1 {
		t.Fatalf("list after insert: rows=%d err=%v", len(rows), err)
	}
	if rows[0].CIDR != "203.0.113.7/32" || rows[0].Reason != "scanner" || rows[0].ExpiresAt == nil {
		t.Fatalf("row mismatch: %+v", rows[0])
	}

	// Same BlockID → update in place (idempotent replay on every replica).
	b2 := Block{BlockID: "abc123", CIDR: "203.0.113.0/24", Reason: "widened", Source: BlockSourceManual}
	if err := repo.UpsertBlock(ctx, &b2); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	rows, _ = repo.ListBlocks(ctx)
	if len(rows) != 1 || rows[0].CIDR != "203.0.113.0/24" || rows[0].Reason != "widened" || rows[0].ExpiresAt != nil {
		t.Fatalf("upsert did not replace in place: %+v", rows)
	}

	if err := repo.DeleteBlockByBlockID(ctx, "abc123"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if rows, _ = repo.ListBlocks(ctx); len(rows) != 0 {
		t.Fatalf("expected empty after delete, got %+v", rows)
	}
}
