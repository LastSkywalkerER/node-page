package raft

import (
	"compress/gzip"
	"database/sql/driver"
	"encoding/gob"
	"fmt"
	"io"
	"sort"
	"time"

	hraft "github.com/hashicorp/raft"
	"gorm.io/gorm"
)

func init() {
	// Rows are dumped as map[string]any, and both drivers scan timestamp
	// columns into concrete time.Time values (pgx for timestamptz, glebarez
	// for sqlite datetime). gob refuses to encode an unregistered concrete
	// type behind an interface — without this registration Persist failed
	// with "gob: type not registered for interface: time.Time" on every
	// attempt, so no snapshot ever succeeded and the raft log grew without
	// bound. Registering here covers BOTH the encode (Persist) and decode
	// (Restore) paths.
	gob.Register(time.Time{})
}

// SQLiteSnapshotter / SQLiteRestorer dump every Raft-managed table out of
// the local SQLite database into a Raft FSM snapshot, and re-import them on
// Restore. The snapshot format is a gzipped gob stream of {tableName,
// rows-as-map[string]any} pairs, written in a stable order so the bytes
// are deterministic across replicas at the same applied index.
//
// Only small identity/config/cluster tables are snapshotted:
//
//   - hosts                       (cluster host registry)
//   - user_accounts               (replicated user accounts)
//   - user_refresh_tokens         (cluster-wide sessions)
//   - cluster_config              (jwt_secret, refresh_secret, bridge cursors, …)
//   - peer_node_advertise         (URL catalog)
//   - cluster_join_tokens         (one-shot bootstrap tokens)
//   - connectors                  (replicated external-source registry; secrets encrypted)
//
// High-churn metric history (cpu/memory/disk/network/docker_metrics and
// docker_container_entities) is DELIBERATELY EXCLUDED from the snapshot.
// Those tables are replicated continuously via CmdMetricBatch and already
// live in every node's own DB; folding them into the snapshot made
// Snapshot() materialise hundreds of MB of history into RAM (as
// map[string][]map[string]any) ON the FSM goroutine — which blocks all
// applies while it dumps, spiking memory and causing "raft: apply: timed
// out enqueuing operation" bursts (the prod incident). A node joining via
// snapshot simply starts with empty metric history and fills forward from
// CmdMetricBatch, which is acceptable.
//
// Other tables (local-only artefacts like the Raft log itself, or per-node
// bridge dedup bookkeeping like applied_remote_log) are not included.

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
	// Connector rows replicate via Raft (CmdConnectorUpsert/Delete) with the
	// token secret AES-GCM-encrypted, so a snapshot-joining node inherits
	// configured integrations. Small and low-churn — safe to snapshot.
	"connectors",
	// NB: cpu/memory/disk/network/docker_metrics and docker_container_entities
	// are intentionally NOT listed here — see the type doc above. They are
	// high-churn metric history replicated via CmdMetricBatch; including them
	// blew up Snapshot()'s RAM use and blocked the FSM goroutine.
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
		// Normalise driver-specific value types into gob-encodable ones
		// BEFORE sorting, so the sort keys (and therefore the bytes) do
		// not depend on which dialect produced the dump.
		normalizeRows(rows)
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
		// SQLite: defer FK checks to commit so the wipe/reinsert can't trip a
		// constraint mid-restore regardless of table order. (Best-effort; harmless
		// when foreign_keys is already off.)
		if name := tx.Dialector.Name(); name == "sqlite" || name == "sqlite3" {
			_ = tx.Exec("PRAGMA defer_foreign_keys = ON").Error
		}
		// Wipe exactly the tables this snapshot stream carries (header.Tables)
		// — NOT the static managedTables set. New snapshots only contain the
		// small config tables, so we must not touch metric history here (it's
		// owned by CmdMetricBatch). An OLD snapshot still carries the metric
		// tables, and truncating each one before its insert keeps that restore
		// conflict-free (insertRows does a plain Create with no OnConflict).
		//
		// Delete BACK-TO-FRONT: managedTables (and the dump order) lists parents
		// before children (user_accounts before user_refresh_tokens), so a
		// front-to-back DELETE removes a parent while its FK child still
		// references it and Postgres rejects it with 23503 — which aborted the
		// whole snapshot restore and left Raft unable to activate. Deleting in
		// reverse removes FK children first; the inserts below stay forward
		// (parent-first), so both phases are FK-safe.
		for i := len(header.Tables) - 1; i >= 0; i-- {
			t := header.Tables[i]
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

// normalizeRows rewrites driver-specific concrete value types in dumped rows
// into gob-encodable ones before Persist encodes them. gob pre-registers the
// numeric/string/bool/[]byte primitives the sql drivers emit; time.Time is
// registered in init above but is converted to UTC here so the encoded bytes
// do not depend on the scanning session's time zone (Postgres returns
// timestamptz in the session zone). Anything else is defensively stringified
// so an exotic driver type can never abort a snapshot again.
func normalizeRows(rows []map[string]any) {
	for _, row := range rows {
		for k, v := range row {
			row[k] = normalizeValue(v)
		}
	}
}

func normalizeValue(v any) any {
	switch t := v.(type) {
	case nil, bool, string, []byte,
		int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64,
		float32, float64:
		return v
	case time.Time:
		return t.UTC()
	case driver.Valuer:
		// GORM's map scan already unwraps Valuers, but guard anyway.
		if dv, err := t.Value(); err == nil {
			if _, again := dv.(driver.Valuer); !again {
				return normalizeValue(dv)
			}
		}
		return fmt.Sprintf("%v", v)
	default:
		return fmt.Sprintf("%v", v)
	}
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
