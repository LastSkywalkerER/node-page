package database

import (
	"context"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"system-stats/internal/platform/gateway"
)

// A deployment that already ran the first gateway_blocks migration has the
// column named c_id_r (NOT NULL). Migrate must rename it rather than add a
// second column next to it — otherwise every insert fails on the old one.
func TestMigrateRenamesLegacyGatewayBlockCIDRColumn(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file::memory:"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	err = db.Exec(`CREATE TABLE gateway_blocks (
		id integer PRIMARY KEY AUTOINCREMENT,
		block_id text NOT NULL,
		c_id_r text NOT NULL,
		reason text, source text, created_by text,
		expires_at datetime, created_at datetime, updated_at datetime)`).Error
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`CREATE UNIQUE INDEX idx_gateway_blocks_block_id ON gateway_blocks(block_id)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	m := db.Migrator()
	if m.HasColumn(&gateway.Block{}, "c_id_r") || !m.HasColumn(&gateway.Block{}, "cidr") {
		t.Fatalf("legacy column not renamed: c_id_r=%v cidr=%v", m.HasColumn(&gateway.Block{}, "c_id_r"), m.HasColumn(&gateway.Block{}, "cidr"))
	}
	repo := gateway.NewBlockRepository(db)
	if err := repo.UpsertBlock(context.Background(), &gateway.Block{BlockID: "x1", CIDR: "198.51.100.0/24", Source: "manual"}); err != nil {
		t.Fatalf("insert after migration: %v", err)
	}
}
