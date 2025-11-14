/**
 * Package database provides database initialization functionality.
 * This package handles database connection setup and is independent of other application modules.
 */
package database

import (
	"fmt"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"system-stats/internal/app/config"
)

/**
 * Initialize creates a new database connection based on the provided configuration.
 * This is a pure function that only depends on the config package.
 *
 * @param dbConfig The database configuration
 * @return *gorm.DB The initialized GORM database instance
 * @return error Returns an error if database connection fails
 */
func Initialize(dbConfig config.DatabaseConfig) (*gorm.DB, error) {
	switch dbConfig.Type {
	case config.DatabaseTypeSQLite:
		return initSQLite(dbConfig)
	default:
		return nil, fmt.Errorf("unsupported database type: %s (supported: sqlite)", dbConfig.Type)
	}
}

/**
 * initSQLite initializes a SQLite database connection.
 *
 * @param dbConfig The database configuration
 * @return *gorm.DB The initialized GORM database instance
 * @return error Returns an error if connection fails
 */
func initSQLite(dbConfig config.DatabaseConfig) (*gorm.DB, error) {
	dsn := dbConfig.DSN
	if dsn == "" {
		dsn = "stats.db" // default SQLite database path
	}

	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		return nil, fmt.Errorf("failed to connect to SQLite database: %w", err)
	}

	// Configure SQLite for better concurrency and integrity
	// Enable WAL mode to reduce write-lock contention and set a busy timeout
	_ = db.Exec("PRAGMA journal_mode=WAL;").Error
	_ = db.Exec("PRAGMA busy_timeout = 5000;").Error
	_ = db.Exec("PRAGMA foreign_keys = ON;").Error

	// Limit max open connections for SQLite to avoid database locked errors
	if sqlDB, err := db.DB(); err == nil {
		// SQLite should generally have 1 writer connection
		sqlDB.SetMaxOpenConns(1)
	}

	return db, nil
}
