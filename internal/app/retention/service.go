package retention

import (
	"context"
	"sync"
	"time"

	"github.com/charmbracelet/log"
	"gorm.io/gorm"

	users "system-stats/internal/auth/users"
)

func timeNowMinusDays(days int) time.Time {
	return time.Now().AddDate(0, 0, -days)
}

var MetricTables = []string{
	"cpu_metrics",
	"memory_metrics",
	"disk_metrics",
	"network_metrics",
	"docker_metrics",
}

// DefaultBatchSize is the number of rows deleted per metric table per CleanupBatch call.
const DefaultBatchSize = 500

// Service deletes metric rows older than RetentionDays in incremental batches,
// driven by the periodic metrics collection hook (no internal ticker). It also
// prunes expired/long-revoked refresh tokens on a slow cadence.
type Service struct {
	db            *gorm.DB
	logger        *log.Logger
	retentionDays int

	// tokenRepo prunes refresh tokens (nilable). Guarded throttle so the cleanup
	// runs at most once per tokenCleanupInterval even though the driving hook
	// fires every few seconds.
	tokenRepo        users.RefreshTokenRepository
	tokenMu          sync.Mutex
	lastTokenCleanup time.Time
}

// tokenCleanupInterval bounds how often refresh-token pruning runs — it needn't
// run every metrics tick.
const tokenCleanupInterval = 10 * time.Minute

func NewService(db *gorm.DB, logger *log.Logger, retentionDays int, tokenRepo users.RefreshTokenRepository) *Service {
	return &Service{db: db, logger: logger, retentionDays: retentionDays, tokenRepo: tokenRepo}
}

// CleanupExpiredTokens prunes expired/long-revoked refresh tokens, throttled to
// tokenCleanupInterval. No-op when no token repo is wired. Called from the same
// metrics-collection hook that drives CleanupBatch.
func (s *Service) CleanupExpiredTokens(ctx context.Context) {
	if s.tokenRepo == nil {
		return
	}
	s.tokenMu.Lock()
	if !s.lastTokenCleanup.IsZero() && time.Since(s.lastTokenCleanup) < tokenCleanupInterval {
		s.tokenMu.Unlock()
		return
	}
	s.lastTokenCleanup = time.Now()
	s.tokenMu.Unlock()

	if err := s.tokenRepo.DeleteExpired(ctx); err != nil && ctx.Err() == nil {
		s.logger.Warn("refresh-token retention cleanup failed", "error", err)
	}
}

// CleanupBatch deletes up to batchSize old rows from each metric table.
// Designed to run frequently (every metrics collection cycle, e.g. 5s) so
// retention work is spread out and never blocks the DB for long.
// The DELETE WHERE id IN (SELECT id ... LIMIT N) shape works on both SQLite and Postgres.
func (s *Service) CleanupBatch(ctx context.Context, batchSize int) (int64, error) {
	if batchSize <= 0 {
		batchSize = DefaultBatchSize
	}
	cutoff := s.cutoffTime()
	var totalDeleted int64
	for _, table := range MetricTables {
		if ctx.Err() != nil {
			return totalDeleted, ctx.Err()
		}
		// Two-step delete: select N old ids, then delete them. Portable across SQLite/Postgres.
		query := "DELETE FROM " + table + " WHERE rowid IN (SELECT rowid FROM " + table + " WHERE timestamp < ? LIMIT ?)"
		if s.db.Dialector.Name() == "postgres" {
			query = "DELETE FROM " + table + " WHERE ctid IN (SELECT ctid FROM " + table + " WHERE timestamp < ? LIMIT ?)"
		}
		res := s.db.WithContext(ctx).Exec(query, cutoff, batchSize)
		if res.Error != nil {
			s.logger.Error("Retention batch failed", "table", table, "error", res.Error)
			continue
		}
		if res.RowsAffected > 0 {
			totalDeleted += res.RowsAffected
			s.logger.Debug("Retention batch", "table", table, "deleted", res.RowsAffected)
		}
	}

	// docker_container_entities is no longer time-series — it is a current-state
	// table (one upserted row per live container, pruned on write when a
	// container disappears). It is bounded by the live container count, so it
	// needs no time-cutoff retention.

	return totalDeleted, nil
}

func (s *Service) cutoffTime() interface{} {
	// Use database-native NOW() arithmetic via raw SQL through cutoff parameter.
	// Keeping it as a Go time keeps things consistent across dialects.
	return timeNowMinusDays(s.retentionDays)
}
