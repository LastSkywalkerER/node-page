package database

import (
	"fmt"

	"gorm.io/gorm"
)

// relaxHostsNameUniqueIndex downgrades the legacy UNIQUE idx_hosts_name index to
// a plain (non-unique) index of the SAME name. The Host.Name column used to
// carry gorm:"uniqueIndex" which created a GLOBAL unique index on hosts.name.
// That uniqueness is wrong: a Proxmox connector guest and/or a cross-cluster
// bridged host can legitimately share a hostname with the local agent host of
// the same name until they are merged by stable identity (mac_address /
// external_id). On Postgres the unique index storms
//
//	duplicate key value violates unique constraint "idx_hosts_name"
//
// on the periodic UPDATE hosts SET name=... WHERE id=N (every ~150ms). The real
// identity keys are mac_address (still uniqueIndex) and external_id.
//
// GORM's AutoMigrate will NOT downgrade an existing unique index to non-unique
// on its own, so this explicit, idempotent, dialect-agnostic step guarantees the
// FINAL state is a non-unique idx_hosts_name regardless of AutoMigrate order. It
// runs AFTER AutoMigrate so the hosts table and the index exist on upgraded DBs;
// on fresh DBs AutoMigrate already creates a non-unique idx_hosts_name and the
// DROP/CREATE is an idempotent no-op (mac_address's uniqueIndex is untouched).
func relaxHostsNameUniqueIndex(db *gorm.DB) error {
	switch db.Dialector.Name() {
	case "postgres":
		return relaxHostsNameUniqueIndexPostgres(db)
	default: // sqlite
		return relaxHostsNameUniqueIndexSQLite(db)
	}
}

// relaxHostsNameUniqueIndexSQLite drops idx_hosts_name (whether it was created
// UNIQUE or not) and re-creates it as a plain index. Both statements are
// IF [NOT] EXISTS guarded so re-running is a no-op.
func relaxHostsNameUniqueIndexSQLite(db *gorm.DB) error {
	// hosts may not exist yet on a brand-new DB if this somehow runs before
	// AutoMigrate; guard so the migration is safe in any order.
	var present int64
	if err := db.Raw(
		"SELECT count(*) FROM sqlite_master WHERE type='table' AND name='hosts'",
	).Scan(&present).Error; err != nil {
		return err
	}
	if present == 0 {
		return nil
	}

	if err := db.Exec(`DROP INDEX IF EXISTS idx_hosts_name`).Error; err != nil {
		return fmt.Errorf("drop idx_hosts_name: %w", err)
	}
	if err := db.Exec(
		`CREATE INDEX IF NOT EXISTS idx_hosts_name ON hosts (name)`,
	).Error; err != nil {
		return fmt.Errorf("create idx_hosts_name: %w", err)
	}
	return nil
}

// relaxHostsNameUniqueIndexPostgres drops idx_hosts_name and re-creates it
// non-unique. GORM's uniqueIndex creates a plain UNIQUE INDEX (not a table
// constraint), so DROP INDEX IF EXISTS removes it. As a defensive fallback —
// in case some DB had it backed by a UNIQUE/PK constraint — we also drop a
// like-named constraint when one exists. Everything is guarded so re-running is
// a no-op.
func relaxHostsNameUniqueIndexPostgres(db *gorm.DB) error {
	// Skip cleanly if the hosts table doesn't exist yet.
	var present bool
	if err := db.Raw(`SELECT to_regclass('public.hosts') IS NOT NULL`).Scan(&present).Error; err != nil {
		return err
	}
	if !present {
		return nil
	}

	// Drop a constraint named idx_hosts_name first if one exists (constraint
	// ownership of the index blocks a plain DROP INDEX). Guarded by a
	// pg_constraint existence check so it's a no-op in the normal case where
	// the index is a plain unique index, not a constraint.
	if err := db.Exec(`
		DO $$
		BEGIN
			IF EXISTS (
				SELECT 1 FROM pg_constraint con
				JOIN pg_class rel ON rel.oid = con.conrelid
				WHERE rel.relname = 'hosts' AND con.conname = 'idx_hosts_name'
			) THEN
				ALTER TABLE hosts DROP CONSTRAINT idx_hosts_name;
			END IF;
		END $$;`).Error; err != nil {
		return fmt.Errorf("drop idx_hosts_name constraint: %w", err)
	}

	// Drop the (unique) index, then recreate it non-unique.
	if err := db.Exec(`DROP INDEX IF EXISTS idx_hosts_name`).Error; err != nil {
		return fmt.Errorf("drop idx_hosts_name: %w", err)
	}
	if err := db.Exec(
		`CREATE INDEX IF NOT EXISTS idx_hosts_name ON hosts (name)`,
	).Error; err != nil {
		return fmt.Errorf("create idx_hosts_name: %w", err)
	}
	return nil
}
