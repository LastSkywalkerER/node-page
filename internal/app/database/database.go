// Package database provides database initialization functionality.
// This package handles database connection setup and is independent of other application modules.
package database

import (
	"fmt"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"system-stats/internal/app/config"
)

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

	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		return nil, fmt.Errorf("failed to connect to SQLite database: %w", err)
	}

	if err := db.Exec("PRAGMA journal_mode=WAL;").Error; err != nil {
		return nil, fmt.Errorf("failed to enable WAL mode: %w", err)
	}
	if err := db.Exec("PRAGMA busy_timeout = 5000;").Error; err != nil {
		return nil, fmt.Errorf("failed to set busy timeout: %w", err)
	}
	if err := db.Exec("PRAGMA foreign_keys = ON;").Error; err != nil {
		return nil, fmt.Errorf("failed to enable foreign keys: %w", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("failed to get sql.DB: %w", err)
	}
	// SQLite supports a single writer to avoid "database is locked" errors.
	sqlDB.SetMaxOpenConns(1)

	return db, nil
}

func initPostgres(dbConfig config.DatabaseConfig) (*gorm.DB, error) {
	if dbConfig.DSN == "" {
		return nil, fmt.Errorf("DB_DSN is required for postgres (e.g. host=localhost user=stats password=... dbname=node_stats port=5432 sslmode=disable)")
	}

	db, err := gorm.Open(postgres.Open(dbConfig.DSN), &gorm.Config{})
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
