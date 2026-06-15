package configdump

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// userAccount maps onto the "user_accounts" managed table with a time column.
type userAccount struct {
	ID           uint `gorm:"primaryKey"`
	Email        string
	PasswordHash string
	Role         string
	CreatedAt    time.Time
}

func (userAccount) TableName() string { return "user_accounts" }

type host struct {
	ID       uint `gorm:"primaryKey"`
	Name     string
	LastSeen time.Time
}

func (host) TableName() string { return "hosts" }

func openDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&userAccount{}, &host{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func TestExportImportRoundTrip(t *testing.T) {
	src := openDB(t)
	src.Create(&userAccount{Email: "admin@example.com", PasswordHash: "$2a$hash", Role: "ADMIN", CreatedAt: time.Now()})
	src.Create(&userAccount{Email: "user@example.com", PasswordHash: "$2a$hash2", Role: "USER", CreatedAt: time.Now()})
	src.Create(&host{Name: "node-1", LastSeen: time.Now()})

	path := filepath.Join(t.TempDir(), DBMigrationDumpFileForTest())
	if err := Export(src, path); err != nil {
		t.Fatalf("export: %v", err)
	}

	// Fresh empty DB == the post-switch database.
	dst := openDB(t)
	imported, err := ImportIfPresent(dst, path)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if !imported {
		t.Fatal("expected imported=true")
	}

	var users []userAccount
	dst.Order("id").Find(&users)
	if len(users) != 2 {
		t.Fatalf("want 2 users, got %d", len(users))
	}
	if users[0].Email != "admin@example.com" || users[0].Role != "ADMIN" || users[0].PasswordHash != "$2a$hash" {
		t.Fatalf("admin row not preserved: %+v", users[0])
	}

	var hosts []host
	dst.Find(&hosts)
	if len(hosts) != 1 || hosts[0].Name != "node-1" {
		t.Fatalf("host row not preserved: %+v", hosts)
	}

	// The dump file is consumed on import.
	if _, err := ImportIfPresent(dst, path); err != nil {
		t.Fatalf("second import: %v", err)
	}
}

func TestImportMissingFileIsNoOp(t *testing.T) {
	dst := openDB(t)
	imported, err := ImportIfPresent(dst, filepath.Join(t.TempDir(), "nope.dump"))
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if imported {
		t.Fatal("expected imported=false for missing file")
	}
}

// DBMigrationDumpFileForTest returns a stable dump filename for tests.
func DBMigrationDumpFileForTest() string { return "db-migrate.dump" }
