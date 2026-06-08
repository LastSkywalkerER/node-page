// Package database provides database initialization functionality.
// This package handles database connection setup and is independent of other application modules.
package database

import (
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"system-stats/internal/app/config"
)

// quietLogger returns a GORM logger that suppresses ErrRecordNotFound noise.
// "record not found" is a normal, expected condition in repository code; logging it
// as a warning on every health-poll or token lookup clutters the output and
// makes real errors harder to spot.
func quietLogger() gormlogger.Interface {
	return gormlogger.New(
		log.New(os.Stdout, "\r\n", log.LstdFlags),
		gormlogger.Config{
			LogLevel:                  gormlogger.Warn,
			IgnoreRecordNotFoundError: true,
		},
	)
}

// Initialize creates a new database connection based on the provided configuration.
func Initialize(dbConfig config.DatabaseConfig) (*gorm.DB, error) {
	switch dbConfig.Type {
	case config.DatabaseTypeSQLite:
		return initSQLite(dbConfig)
	case config.DatabaseTypePostgres:
		return initPostgres(dbConfig)
	default:
		return nil, fmt.Errorf("unsupported database type: %s (supported: sqlite, postgres)", dbConfig.Type)
	}
}

func initSQLite(dbConfig config.DatabaseConfig) (*gorm.DB, error) {
	dsn := dbConfig.DSN
	if dsn == "" {
		dsn = "stats.db"
	}
	// Embed PRAGMAs in the DSN so every connection in the pool (not just the
	// first one) gets WAL mode, a generous busy-timeout, and foreign-key checks.
	// WAL lets multiple readers run alongside one writer; busy_timeout(30000)
	// survives brief lock contention during hot-reloads or concurrent opens.
	// modernc/glebarez applies pragmas via repeated _pragma=name(value) params
	// (the mattn-style "_journal_mode=WAL&_busy_timeout=..." form is ignored).
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	dsn += sep + "_pragma=busy_timeout(30000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(on)"

	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: quietLogger()})
	if err != nil {
		return nil, fmt.Errorf("failed to connect to SQLite database: %w", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("failed to get sql.DB: %w", err)
	}
	// Single connection: SQLite allows only one writer; with one connection
	// all access is serialised by Go's pool with zero SQLite-level contention.
	// busy_timeout in the DSN handles the only real concurrent-access case:
	// a brief overlap when Air hot-reloads the process.
	sqlDB.SetMaxOpenConns(1)

	return db, nil
}

func initPostgres(dbConfig config.DatabaseConfig) (*gorm.DB, error) {
	if dbConfig.DSN == "" {
		return nil, fmt.Errorf("DB_DSN is required for postgres (e.g. host=localhost user=stats password=... dbname=node_stats port=5432 sslmode=disable)")
	}

	db, err := gorm.Open(postgres.Open(dbConfig.DSN), &gorm.Config{Logger: quietLogger()})
	if err != nil {
		return nil, fmt.Errorf("failed to connect to PostgreSQL database: %w", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("failed to get sql.DB: %w", err)
	}
	sqlDB.SetMaxOpenConns(25)
	sqlDB.SetMaxIdleConns(10)
	sqlDB.SetConnMaxLifetime(5 * time.Minute)

	return db, nil
}
