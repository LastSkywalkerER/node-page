package database

import (
	"fmt"

	"gorm.io/gorm"
)

// metricPKTables are the five metric history tables whose primary key is being
// upgraded from a single-column (timestamp) PK to a composite (host_id,
// timestamp) PK. docker_metrics is listed last on purpose: on Postgres its
// PK change must run AFTER the docker_container_entities foreign key that
// references it has been dropped (see migrateMetricCompositePKs).
var metricPKTables = []string{
	"cpu_metrics",
	"memory_metrics",
	"disk_metrics",
	"network_metrics",
	"docker_metrics",
}

// migrateMetricCompositePKs upgrades the metric history tables from a
// timestamp-only primary key to a composite (host_id, timestamp) primary key,
// and drops the unusable fk_docker_metrics_containers foreign key from
// docker_container_entities. It is fully idempotent on both SQLite and
// Postgres — every action is guarded so a second run is a no-op.
//
// It MUST run after AutoMigrate (so the columns/tables exist) but the order of
// operations is significant: the docker_container_entities FK is dropped before
// docker_metrics' PK is rebuilt, because on Postgres a referenced unique
// constraint cannot be dropped while a FK depends on it.
func migrateMetricCompositePKs(db *gorm.DB) error {
	switch db.Dialector.Name() {
	case "postgres":
		return migrateMetricCompositePKsPostgres(db)
	default: // sqlite
		return migrateMetricCompositePKsSQLite(db)
	}
}

// ---------------------------------------------------------------------------
// Postgres
// ---------------------------------------------------------------------------

func migrateMetricCompositePKsPostgres(db *gorm.DB) error {
	// (a) Drop the legacy FK on docker_container_entities first — it points at
	// docker_metrics' old single-column PK and blocks dropping that PK.
	if err := db.Exec(`
		DO $$
		BEGIN
			IF EXISTS (
				SELECT 1 FROM pg_constraint
				WHERE conname = 'fk_docker_metrics_containers'
			) THEN
				ALTER TABLE docker_container_entities
					DROP CONSTRAINT fk_docker_metrics_containers;
			END IF;
		END $$;`).Error; err != nil {
		return fmt.Errorf("drop fk_docker_metrics_containers: %w", err)
	}

	// (b) For every metric table: backfill NULL host_id, enforce NOT NULL,
	// drop the old single-column PK, add the composite PK. Each step is
	// guarded so re-running is a no-op.
	for _, table := range metricPKTables {
		if err := migrateOneMetricTablePostgres(db, table); err != nil {
			return fmt.Errorf("composite pk %s: %w", table, err)
		}
	}
	return nil
}

func migrateOneMetricTablePostgres(db *gorm.DB, table string) error {
	// Backfill any rows whose host_id was never set so the column can become
	// NOT NULL (host id 1 == the local collector host).
	if err := db.Exec(
		fmt.Sprintf(`UPDATE %s SET host_id = 1 WHERE host_id IS NULL`, table),
	).Error; err != nil {
		return err
	}

	// Enforce NOT NULL only if it isn't already (re-running ALTER ... SET NOT
	// NULL is harmless, but guard anyway to keep the migration side-effect-free).
	if err := db.Exec(fmt.Sprintf(`
		DO $$
		BEGIN
			IF EXISTS (
				SELECT 1 FROM information_schema.columns
				WHERE table_name = '%s' AND column_name = 'host_id'
				  AND is_nullable = 'YES'
			) THEN
				ALTER TABLE %s ALTER COLUMN host_id SET NOT NULL;
			END IF;
		END $$;`, table, table)).Error; err != nil {
		return err
	}

	// Determine the existing PK constraint and the set of columns it covers.
	// If it is already the composite (host_id, timestamp) PK, do nothing.
	type pkInfo struct {
		ConName string
		NumCols int
	}
	var info pkInfo
	err := db.Raw(`
		SELECT con.conname AS con_name,
		       cardinality(con.conkey) AS num_cols
		FROM pg_constraint con
		JOIN pg_class rel ON rel.oid = con.conrelid
		WHERE rel.relname = ? AND con.contype = 'p'`, table).Scan(&info).Error
	if err != nil {
		return err
	}

	// No PK at all (shouldn't happen post-AutoMigrate) — add the composite one.
	if info.ConName == "" {
		return db.Exec(fmt.Sprintf(
			`ALTER TABLE %s ADD PRIMARY KEY (host_id, "timestamp")`, table)).Error
	}

	// Already composite — done.
	if info.NumCols >= 2 {
		return nil
	}

	// Single-column PK: drop it, then add the composite PK.
	if err := db.Exec(fmt.Sprintf(
		`ALTER TABLE %s DROP CONSTRAINT %s`, table, info.ConName)).Error; err != nil {
		return err
	}
	return db.Exec(fmt.Sprintf(
		`ALTER TABLE %s ADD PRIMARY KEY (host_id, "timestamp")`, table)).Error
}

// ---------------------------------------------------------------------------
// SQLite
// ---------------------------------------------------------------------------

func migrateMetricCompositePKsSQLite(db *gorm.DB) error {
	// The legacy FK on docker_container_entities references docker_metrics(timestamp)
	// — SQLite enforces it when we DROP docker_metrics during its rebuild. There
	// is no ALTER ... DROP CONSTRAINT in SQLite, so we rebuild the child table
	// WITHOUT the FK first. After this it is the only table referencing a metric
	// table, so the subsequent metric-table rebuilds (DROP + RENAME) no longer
	// trip foreign_keys enforcement and no PRAGMA toggling is needed.
	if err := sqliteDropDockerContainerFK(db); err != nil {
		return fmt.Errorf("drop docker_container_entities fk: %w", err)
	}

	for _, table := range metricPKTables {
		composite, err := sqlitePKIsComposite(db, table)
		if err != nil {
			return fmt.Errorf("inspect pk %s: %w", table, err)
		}
		if composite {
			continue
		}
		if err := sqliteRebuildWithCompositePK(db, table); err != nil {
			return fmt.Errorf("rebuild %s: %w", table, err)
		}
	}
	return nil
}

// sqliteDropDockerContainerFK rebuilds docker_container_entities without any
// foreign-key constraint, preserving its (id, metric_timestamp) composite PK
// and all data. It is a no-op when the table already has no foreign keys
// (fresh DBs created by AutoMigrate with constraint:- never get one).
func sqliteDropDockerContainerFK(db *gorm.DB) error {
	const table = "docker_container_entities"

	// Skip when the table is absent (shouldn't happen post-AutoMigrate) or
	// already FK-free.
	var present int64
	if err := db.Raw(
		"SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?", table,
	).Scan(&present).Error; err != nil {
		return err
	}
	if present == 0 {
		return nil
	}
	type fkRow struct {
		ID int `gorm:"column:id"`
	}
	var fks []fkRow
	if err := db.Raw("PRAGMA foreign_key_list(" + table + ")").Scan(&fks).Error; err != nil {
		return err
	}
	if len(fks) == 0 {
		return nil
	}

	var cols []sqliteColumnInfo
	if err := db.Raw("PRAGMA table_info(" + table + ")").Scan(&cols).Error; err != nil {
		return err
	}

	return db.Transaction(func(tx *gorm.DB) error {
		newTable := table + "_pkmig_new"

		colDDL := ""
		colNames := ""
		for i, c := range cols {
			if i > 0 {
				colDDL += ", "
				colNames += ", "
			}
			ddl := fmt.Sprintf("\"%s\" %s", c.Name, c.Type)
			if c.NotNull == 1 {
				ddl += " NOT NULL"
			}
			colDDL += ddl
			colNames += fmt.Sprintf("\"%s\"", c.Name)
		}

		createSQL := fmt.Sprintf(
			"CREATE TABLE %s (%s, PRIMARY KEY (\"id\", \"metric_timestamp\"))",
			newTable, colDDL)
		if err := tx.Exec(createSQL).Error; err != nil {
			return err
		}
		if err := tx.Exec(fmt.Sprintf(
			"INSERT OR IGNORE INTO %s (%s) SELECT %s FROM %s",
			newTable, colNames, colNames, table)).Error; err != nil {
			return err
		}
		if err := tx.Exec("DROP TABLE " + table).Error; err != nil {
			return err
		}
		return tx.Exec(fmt.Sprintf("ALTER TABLE %s RENAME TO %s", newTable, table)).Error
	})
}

// sqliteColumnInfo mirrors one row of PRAGMA table_info.
type sqliteColumnInfo struct {
	Cid       int     `gorm:"column:cid"`
	Name      string  `gorm:"column:name"`
	Type      string  `gorm:"column:type"`
	NotNull   int     `gorm:"column:notnull"`
	DfltValue *string `gorm:"column:dflt_value"`
	// PK is the 1-based position of the column within the primary key
	// (0 == not part of the PK).
	PK int `gorm:"column:pk"`
}

// sqlitePKIsComposite returns true when the table's primary key already spans
// more than one column (i.e. the composite (host_id, timestamp) PK is in
// place). It reads PRAGMA table_info, where the pk column is the 1-based
// position of each column within the PK (0 == not part of the PK).
func sqlitePKIsComposite(db *gorm.DB, table string) (bool, error) {
	var rows []sqliteColumnInfo
	if err := db.Raw(fmt.Sprintf("PRAGMA table_info(%s)", table)).Scan(&rows).Error; err != nil {
		return false, err
	}
	pkCols := 0
	for _, r := range rows {
		if r.PK > 0 {
			pkCols++
		}
	}
	return pkCols >= 2, nil
}

// sqliteRebuildWithCompositePK rebuilds one metric table with a composite
// (host_id, timestamp) primary key via the standard SQLite table-rebuild dance,
// all inside a single transaction:
//
//	CREATE TABLE <t>_new (... PRIMARY KEY (host_id, timestamp))
//	INSERT OR IGNORE INTO <t>_new SELECT <cols> FROM <t>   (drops dupes)
//	DROP TABLE <t>
//	ALTER TABLE <t>_new RENAME TO <t>
//
// The new table's column list is read from the live table so it works
// regardless of how many metric columns the table has, and the recreated table
// carries the composite PK but no foreign-key constraint (docker_metrics' FK to
// nothing, and docker_container_entities' FK is handled by AutoMigrate which no
// longer declares it). The composite PK's implicit index covers the hot
// (host_id, timestamp) query path immediately; the entities' secondary indexes
// (idx_*_host_ts etc.) are dropped with the old table and re-created by
// AutoMigrate on the next boot.
func sqliteRebuildWithCompositePK(db *gorm.DB, table string) error {
	var cols []sqliteColumnInfo
	if err := db.Raw(fmt.Sprintf("PRAGMA table_info(%s)", table)).Scan(&cols).Error; err != nil {
		return err
	}
	if len(cols) == 0 {
		// Table somehow missing — nothing to rebuild.
		return nil
	}

	return db.Transaction(func(tx *gorm.DB) error {
		newTable := table + "_pkmig_new"

		// Build the column DDL list, forcing host_id NOT NULL so it can be part
		// of the PK. Other columns keep their declared type/null-ability.
		colDDL := ""
		colNames := ""
		for i, c := range cols {
			if i > 0 {
				colDDL += ", "
				colNames += ", "
			}
			ddl := fmt.Sprintf("\"%s\" %s", c.Name, c.Type)
			if c.Name == "host_id" {
				ddl += " NOT NULL"
			} else if c.NotNull == 1 {
				ddl += " NOT NULL"
			}
			colDDL += ddl
			colNames += fmt.Sprintf("\"%s\"", c.Name)
		}

		createSQL := fmt.Sprintf(
			"CREATE TABLE %s (%s, PRIMARY KEY (\"host_id\", \"timestamp\"))",
			newTable, colDDL)
		if err := tx.Exec(createSQL).Error; err != nil {
			return err
		}

		// Backfill NULL host_id to 1 as we copy, and drop any (host_id,
		// timestamp) dupes via INSERT OR IGNORE.
		insertSQL := fmt.Sprintf(
			"INSERT OR IGNORE INTO %s (%s) SELECT %s FROM %s",
			newTable, colNames, selectColsWithHostBackfill(cols), table)
		if err := tx.Exec(insertSQL).Error; err != nil {
			return err
		}

		if err := tx.Exec(fmt.Sprintf("DROP TABLE %s", table)).Error; err != nil {
			return err
		}
		if err := tx.Exec(fmt.Sprintf("ALTER TABLE %s RENAME TO %s", newTable, table)).Error; err != nil {
			return err
		}
		return nil
	})
}

// selectColsWithHostBackfill builds the SELECT column list for the rebuild
// copy, replacing host_id with COALESCE(host_id, 1) so NULL ids become the
// local collector host. Column order matches table_info (== the new table's
// column order).
func selectColsWithHostBackfill(cols []sqliteColumnInfo) string {
	out := ""
	for i, c := range cols {
		if i > 0 {
			out += ", "
		}
		if c.Name == "host_id" {
			out += "COALESCE(\"host_id\", 1)"
		} else {
			out += fmt.Sprintf("\"%s\"", c.Name)
		}
	}
	return out
}
