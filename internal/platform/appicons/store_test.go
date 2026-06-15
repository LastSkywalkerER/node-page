package appicons

import (
	"context"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func newStoreDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&IconCacheEntry{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func TestGormStoreRoundTrip(t *testing.T) {
	st := NewGormStore(newStoreDB(t))
	ctx := context.Background()
	exp := time.Now().Add(time.Hour).Truncate(time.Second)
	st.Save(ctx, "grafana", []byte("<svg/>"), "image/svg+xml", true, exp)

	data, ctype, ok, gotExp, found := st.Load(ctx, "grafana")
	if !found || !ok {
		t.Fatalf("want found+ok, got found=%v ok=%v", found, ok)
	}
	if string(data) != "<svg/>" || ctype != "image/svg+xml" {
		t.Fatalf("bytes/ctype not preserved: %q %q", data, ctype)
	}
	if !gotExp.Equal(exp) {
		t.Fatalf("exp not preserved: %v vs %v", gotExp, exp)
	}

	// Upsert: a second Save for the same key replaces the row, not duplicates it.
	st.Save(ctx, "grafana", []byte("png"), "image/png", true, exp)
	data, ctype, _, _, _ = st.Load(ctx, "grafana")
	if string(data) != "png" || ctype != "image/png" {
		t.Fatalf("upsert did not replace: %q %q", data, ctype)
	}

	if _, _, _, _, found := st.Load(ctx, "missing"); found {
		t.Fatal("missing key should not be found")
	}
}

// TestServiceUsesL2 verifies a process serves bytes from the persistent store
// without resolving against the CDN (no network candidates are configured).
func TestServiceUsesL2(t *testing.T) {
	st := NewGormStore(newStoreDB(t))
	exp := time.Now().Add(time.Hour)
	st.Save(context.Background(), "plausible", []byte("ICON"), "image/png", true, exp)

	svc := NewService(nil).WithStore(st)
	data, ctype, ok := svc.Get(context.Background(), "plausible")
	if !ok || string(data) != "ICON" || ctype != "image/png" {
		t.Fatalf("expected L2 hit, got ok=%v data=%q ctype=%q", ok, data, ctype)
	}
}
