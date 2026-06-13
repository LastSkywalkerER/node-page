package database

import (
	"strings"
	"testing"
)

// TestRelaxHostsNameUniqueIndex seeds a DB with the OLD schema — a UNIQUE
// idx_hosts_name plus one host row — runs Migrate(), and proves:
//  1. a SECOND host row with the SAME name but a DIFFERENT mac_address inserts
//     successfully (the unique(name) constraint is relaxed);
//  2. Migrate() is idempotent (running it twice does not error or re-tighten);
//  3. mac_address is STILL unique (a duplicate mac is rejected).
func TestRelaxHostsNameUniqueIndex(t *testing.T) {
	db := openMem(t)

	// --- Recreate the OLD hosts schema by hand: UNIQUE idx_hosts_name (the
	// pre-fix gorm:"uniqueIndex" shape) plus the unique mac index that must
	// survive. Only the columns the migration/test touch are needed. ---
	if err := db.Exec(`CREATE TABLE hosts (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL,
		mac_address TEXT NOT NULL,
		ipv4 TEXT,
		host_type TEXT,
		parent_mac TEXT,
		source TEXT,
		external_id TEXT,
		guest_status TEXT,
		origin_cluster TEXT,
		boot_time INTEGER,
		last_seen DATETIME,
		created_at DATETIME,
		updated_at DATETIME
	)`).Error; err != nil {
		t.Fatalf("create old hosts: %v", err)
	}
	if err := db.Exec(`CREATE UNIQUE INDEX idx_hosts_name ON hosts (name)`).Error; err != nil {
		t.Fatalf("create unique idx_hosts_name: %v", err)
	}
	if err := db.Exec(`CREATE UNIQUE INDEX idx_hosts_mac_address ON hosts (mac_address)`).Error; err != nil {
		t.Fatalf("create unique idx_hosts_mac_address: %v", err)
	}

	// Seed one host row.
	if err := db.Exec(
		`INSERT INTO hosts (name, mac_address) VALUES (?, ?)`,
		"dokploy", "aa:bb:cc:dd:ee:01",
	).Error; err != nil {
		t.Fatalf("seed first host: %v", err)
	}

	// Precondition: a second row with the SAME name fails under the unique index.
	preErr := db.Exec(
		`INSERT INTO hosts (name, mac_address) VALUES (?, ?)`,
		"dokploy", "aa:bb:cc:dd:ee:02",
	).Error
	if preErr == nil {
		t.Fatalf("precondition: expected duplicate-name insert to fail under UNIQUE idx_hosts_name, but it succeeded")
	}
	if !strings.Contains(strings.ToLower(preErr.Error()), "unique") {
		t.Fatalf("precondition: expected a UNIQUE-constraint error, got %v", preErr)
	}

	// --- Run the full migration. ---
	if err := Migrate(db); err != nil {
		t.Fatalf("Migrate over old schema: %v", err)
	}

	// (1) A second host row with the SAME name but a DIFFERENT mac now inserts.
	if err := db.Exec(
		`INSERT INTO hosts (name, mac_address) VALUES (?, ?)`,
		"dokploy", "aa:bb:cc:dd:ee:02",
	).Error; err != nil {
		t.Fatalf("after migrate: duplicate-name insert should succeed, got %v", err)
	}

	var dokployCount int64
	if err := db.Table("hosts").Where("name = ?", "dokploy").Count(&dokployCount).Error; err != nil {
		t.Fatalf("count dokploy rows: %v", err)
	}
	if dokployCount != 2 {
		t.Fatalf("expected 2 rows named 'dokploy', got %d", dokployCount)
	}

	// (3) mac_address is STILL unique — a duplicate mac is rejected.
	dupMacErr := db.Exec(
		`INSERT INTO hosts (name, mac_address) VALUES (?, ?)`,
		"other-name", "aa:bb:cc:dd:ee:01",
	).Error
	if dupMacErr == nil {
		t.Fatalf("expected duplicate mac_address insert to fail (mac must stay unique), but it succeeded")
	}
	if !strings.Contains(strings.ToLower(dupMacErr.Error()), "unique") {
		t.Fatalf("expected a UNIQUE-constraint error for duplicate mac, got %v", dupMacErr)
	}

	// (2) Idempotent: a second migration must not error and must not re-tighten
	// the name index. Verify by inserting a THIRD same-name row afterwards.
	if err := Migrate(db); err != nil {
		t.Fatalf("second Migrate (idempotency): %v", err)
	}
	if err := db.Exec(
		`INSERT INTO hosts (name, mac_address) VALUES (?, ?)`,
		"dokploy", "aa:bb:cc:dd:ee:03",
	).Error; err != nil {
		t.Fatalf("after 2nd migrate: duplicate-name insert should still succeed, got %v", err)
	}

	// Confirm idx_hosts_name still exists (as a plain, non-unique index) so
	// lookups remain indexed.
	var idxCount int64
	if err := db.Raw(
		`SELECT count(*) FROM sqlite_master WHERE type='index' AND name='idx_hosts_name'`,
	).Scan(&idxCount).Error; err != nil {
		t.Fatalf("query idx_hosts_name presence: %v", err)
	}
	if idxCount != 1 {
		t.Fatalf("expected idx_hosts_name to still exist after migration, count=%d", idxCount)
	}

	// And that it is NOT unique anymore (PRAGMA index_list "unique" flag == 0).
	type idxListRow struct {
		Seq     int    `gorm:"column:seq"`
		Name    string `gorm:"column:name"`
		Unique  int    `gorm:"column:unique"`
		Origin  string `gorm:"column:origin"`
		Partial int    `gorm:"column:partial"`
	}
	var idxRows []idxListRow
	if err := db.Raw(`PRAGMA index_list(hosts)`).Scan(&idxRows).Error; err != nil {
		t.Fatalf("pragma index_list(hosts): %v", err)
	}
	for _, r := range idxRows {
		if r.Name == "idx_hosts_name" && r.Unique != 0 {
			t.Fatalf("idx_hosts_name is still UNIQUE after migration (unique flag=%d)", r.Unique)
		}
	}
}
