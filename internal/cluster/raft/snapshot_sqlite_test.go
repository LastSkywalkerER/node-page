package raft

import (
	"bytes"
	"io"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	hraft "github.com/hashicorp/raft"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// memorySink is an in-memory hraft.SnapshotSink for tests.
type memorySink struct {
	bytes.Buffer
	cancelled bool
	closed    bool
}

var _ hraft.SnapshotSink = (*memorySink)(nil)

func (s *memorySink) ID() string    { return "test-snapshot" }
func (s *memorySink) Cancel() error { s.cancelled = true; return nil }
func (s *memorySink) Close() error  { s.closed = true; return nil }

// snapshotTestHost maps onto the managed "hosts" table with a time.Time
// column, mirroring how the real entities store timestamps.
type snapshotTestHost struct {
	ID       uint `gorm:"primaryKey"`
	Name     string
	LastSeen time.Time
}

func (snapshotTestHost) TableName() string { return "hosts" }

func newSnapshotTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&snapshotTestHost{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

// Regression test: rows dumped as map[string]any carry concrete time.Time
// values (Postgres timestamptz / sqlite datetime scans), which gob refuses
// to encode behind an interface unless registered. Before the fix Persist
// failed with "gob: type not registered for interface: time.Time" and no
// snapshot was ever taken.
func TestSQLiteSnapshot_TimeColumnsRoundTrip(t *testing.T) {
	t.Parallel()

	src := newSnapshotTestDB(t)
	lastSeen := time.Date(2026, 6, 1, 12, 30, 45, 0, time.UTC)
	if err := src.Create(&snapshotTestHost{ID: 1, Name: "hub", LastSeen: lastSeen}).Error; err != nil {
		t.Fatalf("seed row: %v", err)
	}

	snap, err := NewSQLiteSnapshotter(src).Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	sink := &memorySink{}
	if err := snap.Persist(sink); err != nil {
		t.Fatalf("Persist: %v", err)
	}
	snap.Release()
	if sink.cancelled {
		t.Fatal("Persist cancelled the sink")
	}
	if !sink.closed {
		t.Fatal("Persist did not close the sink")
	}
	if sink.Len() == 0 {
		t.Fatal("Persist wrote no bytes")
	}

	dst := newSnapshotTestDB(t)
	rc := io.NopCloser(bytes.NewReader(sink.Bytes()))
	if err := NewSQLiteRestorer(dst).Restore(rc); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	var got snapshotTestHost
	if err := dst.First(&got, 1).Error; err != nil {
		t.Fatalf("read restored row: %v", err)
	}
	if got.Name != "hub" {
		t.Fatalf("restored Name=%q, want %q", got.Name, "hub")
	}
	if !got.LastSeen.Equal(lastSeen) {
		t.Fatalf("restored LastSeen=%v, want %v", got.LastSeen, lastSeen)
	}
}
