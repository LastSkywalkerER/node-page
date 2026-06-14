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
//
// For Postgres it retries transient connection failures (e.g. the DB container
// is still starting and not yet accepting connections) with a bounded
// exponential backoff: up to 24 attempts, 1s,2s,...capped at 10s (~3-4 min
// total). Configuration/credential errors (bad DSN, auth failure) are not
// transient and fail fast. SQLite is a local file and never retries.
func Initialize(dbConfig config.DatabaseConfig) (*gorm.DB, error) {
	switch dbConfig.Type {
	case config.DatabaseTypeSQLite:
		return initSQLite(dbConfig)
	case config.DatabaseTypePostgres:
		return initPostgresWithRetry(dbConfig)
	default:
		return nil, fmt.Errorf("unsupported database type: %s (supported: sqlite, postgres)", dbConfig.Type)
	}
}

const (
	dbConnectMaxAttempts = 24
	dbConnectMaxBackoff  = 10 * time.Second
)

// isTransientDBError reports whether a Postgres connection error is worth
// retrying. We treat "server not yet accepting connections" / connection
// refused / network timeouts as transient (the DB is still coming up), but
// authentication and bad-config errors as fatal so we don't loop on a
// misconfiguration. The pgx driver does not expose stable typed errors here
// without importing it directly, so we match on the (stable) message text.
func isTransientDBError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	// Fatal: credentials / database / config problems — never retry these.
	for _, fatal := range []string{
		"password authentication failed",
		"authentication failed",
		"role \"",
		"does not exist",
		"invalid dsn",
		"cannot parse",
		"ssl is not enabled",
		"no pg_hba.conf entry",
	} {
		if strings.Contains(msg, fatal) {
			return false
		}
	}
	// Transient: server still starting / unreachable.
	for _, transient := range []string{
		"not yet accepting connections",
		"the database system is starting up",
		"connection refused",
		"connection reset",
		"no such host",
		"i/o timeout",
		"timeout",
		"dial tcp",
		"econnrefused",
		"server closed the connection",
		"network is unreachable",
		"no route to host",
	} {
		if strings.Contains(msg, transient) {
			return true
		}
	}
	return false
}

func initPostgresWithRetry(dbConfig config.DatabaseConfig) (*gorm.DB, error) {
	var lastErr error
	backoff := time.Second
	for attempt := 1; attempt <= dbConnectMaxAttempts; attempt++ {
		db, err := initPostgres(dbConfig)
		if err == nil {
			return db, nil
		}
		lastErr = err
		if !isTransientDBError(err) || attempt == dbConnectMaxAttempts {
			return nil, err
		}
		log.Printf("[WARN] database: connection attempt %d/%d failed (%v); retrying in %s",
			attempt, dbConnectMaxAttempts, err, backoff)
		time.Sleep(backoff)
		if backoff < dbConnectMaxBackoff {
			backoff *= 2
			if backoff > dbConnectMaxBackoff {
				backoff = dbConnectMaxBackoff
			}
		}
	}
	return nil, lastErr
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
	// This is a periodic-write monitoring app, not a high-concurrency OLTP
	// service: a handful of queries run at once (metric persist, retention, the
	// dashboard's SELECTs, SSE fan-out). Every open connection is a full Postgres
	// backend process holding its own private memory until closed, so a fat idle
	// pool is pure resident-RAM cost with no throughput gain — the dominant
	// "Postgres RAM grows over time" lever on managed deployments where we can't
	// touch shared_buffers (dokploy). Cap the ceiling, keep the idle floor tiny,
	// and reap idle backends quickly so their RAM is returned between bursts.
	// ConnMaxLifetime still recycles active backends to bound per-backend cache.
	sqlDB.SetMaxOpenConns(10)
	sqlDB.SetMaxIdleConns(2)
	sqlDB.SetConnMaxIdleTime(2 * time.Minute)
	sqlDB.SetConnMaxLifetime(5 * time.Minute)

	return db, nil
}
