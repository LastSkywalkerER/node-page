package raft

import (
	"compress/gzip"
	"encoding/gob"
	"fmt"
	"io"
	"sort"

	hraft "github.com/hashicorp/raft"
	"gorm.io/gorm"
)

// SQLiteSnapshotter / SQLiteRestorer dump every Raft-managed table out of
// the local SQLite database into a Raft FSM snapshot, and re-import them on
// Restore. The snapshot format is a gzipped gob stream of {tableName,
// rows-as-map[string]any} pairs, written in a stable order so the bytes
// are deterministic across replicas at the same applied index.
//
// Tables managed by Raft (and therefore covered by the snapshot):
//
//   - hosts                       (cluster host registry)
//   - users                       (replicated user accounts)
//   - refresh_tokens              (cluster-wide sessions)
//   - cluster_config              (jwt_secret, refresh_secret, bridge cursors, …)
//   - peer_node_advertise         (URL catalog)
//   - cluster_join_tokens         (one-shot bootstrap tokens)
//   - cpu_metrics                 (replicated metric history)
//   - memory_metrics
//   - disk_metrics
//   - network_metrics
//   - docker_metrics
//   - docker_container_entities
//
// Other tables (local-only artefacts like the Raft log itself) are not
// included.

var managedTables = []string{
	"hosts",
	// NB: the Go entities override gorm's default names — User lives in
	// "user_accounts", RefreshToken in "user_refresh_tokens". The dump
	// listed "users"/"refresh_tokens" for a long time: sqlite silently
	// treated the missing tables as empty (snapshots shipped WITHOUT
	// accounts/sessions), while Postgres aborts the whole transaction on
	// the first failed SELECT — no snapshot could ever be taken and the
	// raft log grew without bound.
	"user_accounts",
	"user_refresh_tokens",
	"cluster_config",
	"peer_node_advertise",
	"cluster_join_tokens",
	"cpu_metrics",
	"memory_metrics",
	"disk_metrics",
	"network_metrics",
	"docker_metrics",
	// GORM's default table name for DockerContainerEntity (NOT "docker_containers").
	"docker_container_entities",
}

// SQLiteSnapshotter implements Snapshotter for the GORM-backed DB.
type SQLiteSnapshotter struct{ db *gorm.DB }

// NewSQLiteSnapshotter wires the snapshotter.
func NewSQLiteSnapshotter(db *gorm.DB) *SQLiteSnapshotter { return &SQLiteSnapshotter{db: db} }

// Snapshot implements Snapshotter.
func (s *SQLiteSnapshotter) Snapshot() (hraft.FSMSnapshot, error) {
	if s.db == nil {
		return emptySnapshot{}, nil
	}
	// Build the snapshot in-memory under a single read transaction so the
	// dump is internally consistent. For SQLite this is BEGIN IMMEDIATE
	// semantics; GORM's Transaction wraps that.
	tables := make(map[string][]map[string]any, len(managedTables))
	err := s.db.Transaction(func(tx *gorm.DB) error {
		for _, t := range managedTables {
			// Probe existence BEFORE selecting: inside a Postgres
			// transaction a single failed statement aborts everything
			// after it, so the isNoSuchTable tolerance in dumpTable can
			// never fire safely mid-transaction there.
			if !tx.Migrator().HasTable(t) {
				tables[t] = nil
				continue
			}
			rows, err := dumpTable(tx, t)
			if err != nil {
				return fmt.Errorf("dump table %s: %w", t, err)
			}
			tables[t] = rows
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &sqliteSnapshot{tables: tables}, nil
}

type sqliteSnapshot struct {
	tables map[string][]map[string]any
}

// Persist implements hraft.FSMSnapshot. It serialises every managed table
// in a fixed, sorted order so two replicas at the same applied index
// produce byte-identical snapshots.
func (s *sqliteSnapshot) Persist(sink hraft.SnapshotSink) error {
	gz := gzip.NewWriter(sink)
	enc := gob.NewEncoder(gz)

	keys := make([]string, 0, len(s.tables))
	for k := range s.tables {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	header := snapshotHeader{Version: 1, Tables: keys}
	if err := enc.Encode(header); err != nil {
		_ = sink.Cancel()
		return err
	}
	for _, name := range keys {
		rows := s.tables[name]
		// Sort each table by its full row signature so determinism does
		// not depend on the storage engine's iteration order.
		sortRows(rows)
		if err := enc.Encode(snapshotTable{Name: name, Rows: rows}); err != nil {
			_ = sink.Cancel()
			return err
		}
	}
	if err := gz.Close(); err != nil {
		_ = sink.Cancel()
		return err
	}
	return sink.Close()
}

// Release implements hraft.FSMSnapshot.
func (s *sqliteSnapshot) Release() {}

type snapshotHeader struct {
	Version int
	Tables  []string
}

type snapshotTable struct {
	Name string
	Rows []map[string]any
}

// SQLiteRestorer implements Restorer.
type SQLiteRestorer struct{ db *gorm.DB }

// NewSQLiteRestorer wires the restorer.
func NewSQLiteRestorer(db *gorm.DB) *SQLiteRestorer { return &SQLiteRestorer{db: db} }

// Restore implements Restorer. It truncates every managed table and bulk-
// inserts the snapshot contents under a single transaction.
func (r *SQLiteRestorer) Restore(rc io.ReadCloser) error {
	defer rc.Close()
	if r.db == nil {
		_, _ = io.Copy(io.Discard, rc)
		return nil
	}

	gz, err := gzip.NewReader(rc)
	if err != nil {
		return fmt.Errorf("snapshot: gzip reader: %w", err)
	}
	defer gz.Close()

	dec := gob.NewDecoder(gz)
	var header snapshotHeader
	if err := dec.Decode(&header); err != nil {
		return fmt.Errorf("snapshot: decode header: %w", err)
	}

	return r.db.Transaction(func(tx *gorm.DB) error {
		// Wipe in reverse FK order — docker_container_entities FKs onto
		// docker_metrics, etc. — but we use raw DELETE so order
		// flexibility matters less than disabling triggers/FKs would.
		for _, t := range managedTables {
			if !tx.Migrator().HasTable(t) {
				continue
			}
			if err := tx.Exec(fmt.Sprintf("DELETE FROM %s", t)).Error; err != nil {
				return fmt.Errorf("snapshot restore: truncate %s: %w", t, err)
			}
		}
		for range header.Tables {
			var st snapshotTable
			if err := dec.Decode(&st); err != nil {
				return fmt.Errorf("snapshot restore: decode table: %w", err)
			}
			// Old snapshots may carry the legacy "users"/"refresh_tokens"
			// names (or tables this build doesn't know) — skip instead of
			// aborting the restore transaction on Postgres.
			if !tx.Migrator().HasTable(st.Name) {
				continue
			}
			if err := insertRows(tx, st.Name, st.Rows); err != nil {
				return fmt.Errorf("snapshot restore: insert %s: %w", st.Name, err)
			}
		}
		return nil
	})
}

// dumpTable reads every row from the named table as a generic map. Done
// with raw SQL so it works across SQLite and PostgreSQL without needing
// per-entity Go types here.
func dumpTable(tx *gorm.DB, table string) ([]map[string]any, error) {
	rows := []map[string]any{}
	if err := tx.Table(table).Find(&rows).Error; err != nil {
		// Tables created in a later migration may not exist on older
		// installations; treat that as "empty" rather than an error so
		// snapshots stay forward-compatible.
		if isNoSuchTable(err) {
			return nil, nil
		}
		return nil, err
	}
	return rows, nil
}

// insertRows bulk-inserts a slice of map-rows into table via Create. GORM
// translates each map into a parameterised INSERT.
func insertRows(tx *gorm.DB, table string, rows []map[string]any) error {
	if len(rows) == 0 {
		return nil
	}
	const chunk = 200
	for i := 0; i < len(rows); i += chunk {
		end := i + chunk
		if end > len(rows) {
			end = len(rows)
		}
		if err := tx.Table(table).Create(rows[i:end]).Error; err != nil {
			if isNoSuchTable(err) {
				return nil
			}
			return err
		}
	}
	return nil
}

func isNoSuchTable(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return contains(msg, "no such table") || contains(msg, "does not exist")
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || (len(sub) > 0 && (indexOf(s, sub) >= 0)))
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// sortRows orders a row slice by the joined values of its keys (sorted),
// giving every replica the same byte layout for the snapshot.
func sortRows(rows []map[string]any) {
	if len(rows) < 2 {
		return
	}
	// Pre-compute a stable comparison key per row.
	keys := make([]string, len(rows))
	if len(rows) > 0 {
		colNames := make([]string, 0, len(rows[0]))
		for k := range rows[0] {
			colNames = append(colNames, k)
		}
		sort.Strings(colNames)
		for i, row := range rows {
			var buf []byte
			for _, c := range colNames {
				buf = append(buf, []byte(c)...)
				buf = append(buf, '=')
				buf = append(buf, []byte(fmt.Sprintf("%v", row[c]))...)
				buf = append(buf, '|')
			}
			keys[i] = string(buf)
		}
	}
	// Indirect sort.
	idx := make([]int, len(rows))
	for i := range idx {
		idx[i] = i
	}
	sort.Slice(idx, func(a, b int) bool { return keys[idx[a]] < keys[idx[b]] })
	out := make([]map[string]any, len(rows))
	for i, j := range idx {
		out[i] = rows[j]
	}
	copy(rows, out)
}
