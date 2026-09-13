package raft

import (
	"bytes"
	"io"
	"strings"
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

	snap, err := NewSQLiteSnapshotter(src).Snapshot(0)
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
	if _, err := NewSQLiteRestorer(dst).Restore(rc, 0); err != nil {
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

// snapAccount / snapRefreshToken model the user_accounts ← user_refresh_tokens
// foreign key. Deleting a parent while a child references it raises FK 23503 on
// Postgres (and an FK error on SQLite with foreign_keys ON).
type snapAccount struct {
	ID       uint `gorm:"primaryKey"`
	Username string
}

func (snapAccount) TableName() string { return "user_accounts" }

type snapRefreshToken struct {
	ID      uint `gorm:"primaryKey"`
	UserID  uint
	Account snapAccount `gorm:"foreignKey:UserID;references:ID;constraint:OnDelete:RESTRICT"`
}

func (snapRefreshToken) TableName() string { return "user_refresh_tokens" }

// TestSQLiteRestore_ForeignKeyWipeOrder is the regression for the snapshot
// restore that left Raft unable to activate: the restore wiped user_accounts
// before user_refresh_tokens, but the refresh-token FK references it, so the
// parent DELETE was rejected (FK violation) and the whole restore aborted. The
// fix deletes back-to-front (children first). This restores into a destination
// that ALREADY holds FK-linked rows, with FK enforcement ON, so a wrong order
// fails the test.
func TestSQLiteRestore_ForeignKeyWipeOrder(t *testing.T) {
	open := func() *gorm.DB {
		db, err := gorm.Open(sqlite.Open("file::memory:?_pragma=foreign_keys(on)"), &gorm.Config{Logger: logger.Discard})
		if err != nil {
			t.Fatalf("open sqlite: %v", err)
		}
		if err := db.AutoMigrate(&snapshotTestHost{}, &snapAccount{}, &snapRefreshToken{}); err != nil {
			t.Fatalf("migrate: %v", err)
		}
		return db
	}

	src := open()
	src.Create(&snapAccount{ID: 1, Username: "admin"})
	src.Create(&snapRefreshToken{ID: 10, UserID: 1})

	snap, err := NewSQLiteSnapshotter(src).Snapshot(0)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	sink := &memorySink{}
	if err := snap.Persist(sink); err != nil {
		t.Fatalf("Persist: %v", err)
	}

	// Destination already has DIFFERENT FK-linked rows, so the restore must wipe
	// a parent that a child still references.
	dst := open()
	dst.Create(&snapAccount{ID: 2, Username: "stale"})
	dst.Create(&snapRefreshToken{ID: 20, UserID: 2})

	rc := io.NopCloser(bytes.NewReader(sink.Bytes()))
	if _, err := NewSQLiteRestorer(dst).Restore(rc, 0); err != nil {
		t.Fatalf("Restore must not fail on the FK wipe order: %v", err)
	}

	var accounts []snapAccount
	dst.Find(&accounts)
	if len(accounts) != 1 || accounts[0].ID != 1 || accounts[0].Username != "admin" {
		t.Fatalf("restored accounts = %+v, want only {1 admin}", accounts)
	}
	var tokens []snapRefreshToken
	dst.Find(&tokens)
	if len(tokens) != 1 || tokens[0].ID != 10 || tokens[0].UserID != 1 {
		t.Fatalf("restored tokens = %+v, want only {10 ->1}", tokens)
	}
}

// High-churn metric history must stay OUT of the FSM snapshot: it is
// replicated continuously via CmdMetricBatch and lives in each node's own
// DB. Including it made Snapshot() materialise hundreds of MB into RAM on
// the FSM goroutine, blocking applies and spiking memory (the prod
// incident). This guards against re-adding any of those tables.
func TestManagedTables_ExcludeMetricHistory(t *testing.T) {
	t.Parallel()

	excluded := []string{
		"cpu_metrics",
		"memory_metrics",
		"disk_metrics",
		"network_metrics",
		"docker_metrics",
		"docker_container_entities",
	}
	for _, table := range managedTables {
		for _, bad := range excluded {
			if table == bad {
				t.Errorf("metric table %q must not be in managedTables (snapshot set)", bad)
			}
		}
	}

	// And the small identity/config tables must remain covered.
	wantPresent := []string{
		"hosts",
		"user_accounts",
		"user_refresh_tokens",
		"cluster_config",
		"peer_node_advertise",
		"cluster_join_tokens",
		"connectors",
	}
	present := make(map[string]bool, len(managedTables))
	for _, t := range managedTables {
		present[t] = true
	}
	for _, want := range wantPresent {
		if !present[want] {
			t.Errorf("config table %q missing from managedTables (snapshot set)", want)
		}
	}
}

// A new snapshot taken on a DB that still has metric tables must NOT carry
// them — Snapshot() only dumps managedTables, so the metric history is
// never materialised into the snapshot stream.
func TestSQLiteSnapshot_OmitsMetricTables(t *testing.T) {
	t.Parallel()

	db := newSnapshotTestDB(t)
	// Create a metric-like table and seed it; it must be ignored by Snapshot.
	if err := db.Exec("CREATE TABLE cpu_metrics (id INTEGER PRIMARY KEY, usage REAL)").Error; err != nil {
		t.Fatalf("create cpu_metrics: %v", err)
	}
	if err := db.Exec("INSERT INTO cpu_metrics (id, usage) VALUES (1, 42.5)").Error; err != nil {
		t.Fatalf("seed cpu_metrics: %v", err)
	}
	if err := db.Create(&snapshotTestHost{ID: 1, Name: "hub", LastSeen: time.Now().UTC()}).Error; err != nil {
		t.Fatalf("seed host: %v", err)
	}

	snap, err := NewSQLiteSnapshotter(db).Snapshot(0)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	s, ok := snap.(*sqliteSnapshot)
	if !ok {
		t.Fatalf("snapshot is %T, want *sqliteSnapshot", snap)
	}
	if _, present := s.tables["cpu_metrics"]; present {
		t.Fatal("cpu_metrics must not be present in the snapshot table set")
	}
	if _, present := s.tables["hosts"]; !present {
		t.Fatal("hosts must be present in the snapshot table set")
	}
}

// snapshotBytes dumps src at appliedIndex and returns the serialised snapshot.
func snapshotBytes(t *testing.T, src *gorm.DB, appliedIndex uint64) []byte {
	t.Helper()
	snap, err := NewSQLiteSnapshotter(src).Snapshot(appliedIndex)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	sink := &memorySink{}
	if err := snap.Persist(sink); err != nil {
		t.Fatalf("Persist: %v", err)
	}
	snap.Release()
	return sink.Bytes()
}

// raft restores the newest snapshot on EVERY start. Our FSM is the database
// that just survived the restart, and snapshots here are rare, so obeying that
// blindly would rewind days of state — and then re-apply the whole log tail on
// top of it, which is how a machine that moved came back as a ghost row.
func TestSQLiteRestore_SkippedWhenLocalStateIsAhead(t *testing.T) {
	t.Parallel()

	src := newSnapshotTestDB(t)
	if err := src.Create(&snapshotTestHost{ID: 1, Name: "old-name"}).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}
	data := snapshotBytes(t, src, 100)

	// The local node is PAST that snapshot: it has the newer name and a higher
	// watermark.
	dst := newSnapshotTestDB(t)
	if err := dst.Create(&snapshotTestHost{ID: 1, Name: "new-name"}).Error; err != nil {
		t.Fatalf("seed dst: %v", err)
	}

	res, err := NewSQLiteRestorer(dst).Restore(io.NopCloser(bytes.NewReader(data)), 140)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if !res.Skipped {
		t.Fatal("restore rewound a database that was already ahead of the snapshot")
	}
	if res.Index != 100 {
		t.Fatalf("res.Index = %d, want 100", res.Index)
	}
	var got snapshotTestHost
	if err := dst.First(&got, 1).Error; err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.Name != "new-name" {
		t.Fatalf("local row was overwritten: %q", got.Name)
	}
}

// A snapshot that IS ahead (a fresh joiner, a node that fell behind its peers'
// log, a wiped database) must still be taken in full.
func TestSQLiteRestore_AppliedWhenSnapshotIsAhead(t *testing.T) {
	t.Parallel()

	src := newSnapshotTestDB(t)
	if err := src.Create(&snapshotTestHost{ID: 1, Name: "from-leader"}).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}
	data := snapshotBytes(t, src, 900)

	dst := newSnapshotTestDB(t)
	if err := dst.Create(&snapshotTestHost{ID: 2, Name: "stale-local"}).Error; err != nil {
		t.Fatalf("seed dst: %v", err)
	}

	res, err := NewSQLiteRestorer(dst).Restore(io.NopCloser(bytes.NewReader(data)), 120)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if res.Skipped {
		t.Fatal("restore was skipped although the snapshot was ahead")
	}
	if res.Index != 900 {
		t.Fatalf("res.Index = %d, want 900", res.Index)
	}
	var names []string
	if err := dst.Model(&snapshotTestHost{}).Pluck("name", &names).Error; err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(names) != 1 || names[0] != "from-leader" {
		t.Fatalf("tables not replaced by the snapshot: %v", names)
	}
}

// A snapshot written before the index existed says nothing about where it sits,
// so it is always taken and the caller is told "unknown" (Index 0).
func TestSQLiteRestore_SnapshotWithoutIndexAlwaysApplies(t *testing.T) {
	t.Parallel()

	src := newSnapshotTestDB(t)
	if err := src.Create(&snapshotTestHost{ID: 1, Name: "legacy"}).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}
	data := snapshotBytes(t, src, 0)

	dst := newSnapshotTestDB(t)
	res, err := NewSQLiteRestorer(dst).Restore(io.NopCloser(bytes.NewReader(data)), 5000)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if res.Skipped || res.Index != 0 {
		t.Fatalf("res = %+v, want an applied restore with an unknown index", res)
	}
}

// stubRestorer lets the FSM-level watermark behaviour be exercised without a DB.
type stubRestorer struct {
	res        RestoreResult
	localSeen  uint64
	restoreErr error
}

func (s *stubRestorer) Restore(rc io.ReadCloser, localIndex uint64) (RestoreResult, error) {
	_ = rc.Close()
	s.localSeen = localIndex
	return s.res, s.restoreErr
}

// After a restore the watermark must describe the state that is actually in the
// database: the snapshot's index when it was taken, unchanged when it was
// skipped, and "nothing proven" for a snapshot that carries no index.
func TestFSM_RestoreRealignsTheWatermark(t *testing.T) {
	t.Parallel()

	newFSM := func(local uint64) (*FSM, *memAppliedIndexStore) {
		fsm := NewFSM(newTestLogger(t))
		store := &memAppliedIndexStore{idx: local}
		fsm.SetAppliedIndexStore(store)
		return fsm, store
	}

	t.Run("applied", func(t *testing.T) {
		fsm, store := newFSM(120)
		st := &stubRestorer{res: RestoreResult{Index: 900}}
		fsm.SetRestorer(st)
		if err := fsm.Restore(io.NopCloser(strings.NewReader(""))); err != nil {
			t.Fatalf("Restore: %v", err)
		}
		if st.localSeen != 120 {
			t.Fatalf("restorer saw localIndex=%d, want 120", st.localSeen)
		}
		if fsm.DurableIndex() != 900 || store.idx != 900 {
			t.Fatalf("watermark = %d (persisted %d), want 900", fsm.DurableIndex(), store.idx)
		}
	})

	t.Run("skipped", func(t *testing.T) {
		fsm, _ := newFSM(140)
		fsm.SetRestorer(&stubRestorer{res: RestoreResult{Index: 100, Skipped: true}})
		if err := fsm.Restore(io.NopCloser(strings.NewReader(""))); err != nil {
			t.Fatalf("Restore: %v", err)
		}
		if fsm.DurableIndex() != 140 {
			t.Fatalf("watermark = %d after a skipped restore, want 140", fsm.DurableIndex())
		}
	})

	t.Run("unknown index", func(t *testing.T) {
		fsm, store := newFSM(140)
		fsm.SetRestorer(&stubRestorer{res: RestoreResult{}})
		if err := fsm.Restore(io.NopCloser(strings.NewReader(""))); err != nil {
			t.Fatalf("Restore: %v", err)
		}
		if fsm.DurableIndex() != 0 || store.idx != 0 {
			t.Fatalf("watermark = %d (persisted %d), want 0 — nothing is proven about the restored state", fsm.DurableIndex(), store.idx)
		}
	})
}
