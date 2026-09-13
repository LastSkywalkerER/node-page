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

	metricMu          sync.Mutex
	lastMetricCleanup time.Time

	iconMu          sync.Mutex
	lastIconCleanup time.Time

	orphanMu          sync.Mutex
	lastOrphanCleanup time.Time
}

// RunDue runs all retention chores that are due, each self-throttled: metric
// pruning every metricCleanupInterval and token pruning every
// tokenCleanupInterval. Safe to call from the metrics hook every tick — most
// calls are cheap no-ops. Errors are logged, never returned.
func (s *Service) RunDue(ctx context.Context) {
	s.metricMu.Lock()
	due := s.lastMetricCleanup.IsZero() || time.Since(s.lastMetricCleanup) >= metricCleanupInterval
	if due {
		s.lastMetricCleanup = time.Now()
	}
	s.metricMu.Unlock()
	if due {
		if _, err := s.CleanupBatch(ctx, metricCleanupBatch); err != nil && ctx.Err() == nil {
			s.logger.Warn("Retention batch error", "error", err)
		}
	}
	s.CleanupExpiredTokens(ctx)
	s.CleanupExpiredIcons(ctx)
	s.CleanupOrphanMetrics(ctx)
}

// orphanCleanupInterval / orphanCleanupBatch bound the orphan sweep. It exists
// because a host delete now removes the hosts row first and drains its history
// afterwards (see hosts.DeleteHostCascade): whatever the purge did not finish —
// it runs inside a replicated applier's budget — is left for this sweep, which
// has all the time in the world. It also collects history left behind by older
// versions, where a cascade that ran out of context abandoned both the rows and
// the row's own deletion.
const (
	orphanCleanupInterval = 5 * time.Minute
	orphanCleanupBatch    = 5000
)

// CleanupOrphanMetrics deletes metric rows whose host_id no longer exists,
// throttled to orphanCleanupInterval and bounded to one batch per table per
// run. Work is derived from the data itself (the distinct host_ids present
// versus the hosts table), so there is no queue to keep in sync and a sweep
// interrupted half-way simply continues next time. Errors are logged.
func (s *Service) CleanupOrphanMetrics(ctx context.Context) {
	s.orphanMu.Lock()
	if !s.lastOrphanCleanup.IsZero() && time.Since(s.lastOrphanCleanup) < orphanCleanupInterval {
		s.orphanMu.Unlock()
		return
	}
	s.lastOrphanCleanup = time.Now()
	s.orphanMu.Unlock()

	var liveIDs []uint
	if err := s.db.WithContext(ctx).Table("hosts").Pluck("id", &liveIDs).Error; err != nil {
		if ctx.Err() == nil {
			s.logger.Warn("orphan metric sweep: list hosts failed", "error", err)
		}
		return
	}
	// An empty hosts table is not a licence to wipe every metric: it reads far
	// more like a half-migrated or mid-restore database than like a node that
	// genuinely knows about no machines.
	if len(liveIDs) == 0 {
		return
	}
	live := make(map[uint]struct{}, len(liveIDs))
	for _, id := range liveIDs {
		live[id] = struct{}{}
	}

	rowKey := "rowid"
	if s.db.Dialector.Name() == "postgres" {
		rowKey = "ctid"
	}
	tables := append(append([]string{}, MetricTables...), "docker_container_entities")
	for _, table := range tables {
		if ctx.Err() != nil {
			return
		}
		if !s.db.Migrator().HasTable(table) {
			continue
		}
		var present []uint
		// host_id is indexed on every one of these tables, so the distinct scan
		// is cheap next to the deletes it saves.
		if err := s.db.WithContext(ctx).Table(table).Distinct().Pluck("host_id", &present).Error; err != nil {
			if ctx.Err() == nil {
				s.logger.Warn("orphan metric sweep: scan failed", "table", table, "error", err)
			}
			continue
		}
		for _, hostID := range present {
			if _, ok := live[hostID]; ok || ctx.Err() != nil {
				continue
			}
			query := "DELETE FROM " + table + " WHERE " + rowKey + " IN (SELECT " + rowKey +
				" FROM " + table + " WHERE host_id = ? LIMIT ?)"
			res := s.db.WithContext(ctx).Exec(query, hostID, orphanCleanupBatch)
			if res.Error != nil {
				if ctx.Err() == nil {
					s.logger.Warn("orphan metric sweep failed", "table", table, "host_id", hostID, "error", res.Error)
				}
				continue
			}
			if res.RowsAffected > 0 {
				s.logger.Info("orphan metric sweep", "table", table, "host_id", hostID, "deleted", res.RowsAffected)
			}
		}
	}
}

// iconCleanupInterval bounds how often the app-icon cache is pruned — entries
// have a 24h TTL, so an hourly sweep keeps the (small) table from accumulating
// long-stale rows without running every tick.
const iconCleanupInterval = time.Hour

// CleanupExpiredIcons deletes app_icon_cache rows past their expiry, throttled
// to iconCleanupInterval. The table is small and regenerable, so a single
// unbatched DELETE is fine on both SQLite and Postgres. Errors are logged.
func (s *Service) CleanupExpiredIcons(ctx context.Context) {
	s.iconMu.Lock()
	if !s.lastIconCleanup.IsZero() && time.Since(s.lastIconCleanup) < iconCleanupInterval {
		s.iconMu.Unlock()
		return
	}
	s.lastIconCleanup = time.Now()
	s.iconMu.Unlock()

	// Skip cleanly when the cache table doesn't exist (e.g. a build/test without
	// the migration) instead of logging a spurious error every hour.
	if !s.db.Migrator().HasTable("app_icon_cache") {
		return
	}
	res := s.db.WithContext(ctx).Exec("DELETE FROM app_icon_cache WHERE expires_at < ?", time.Now())
	if res.Error != nil && ctx.Err() == nil {
		s.logger.Warn("app-icon cache cleanup failed", "error", res.Error)
	} else if res.RowsAffected > 0 {
		s.logger.Debug("app-icon cache cleanup", "deleted", res.RowsAffected)
	}
}

// tokenCleanupInterval bounds how often refresh-token pruning runs — it needn't
// run every metrics tick.
const tokenCleanupInterval = 10 * time.Minute

// metricCleanupInterval throttles metric retention: instead of a small DELETE
// every collection tick (which thrashes autovacuum with many tiny statements),
// run a larger batch every few minutes. The dead-tuple VOLUME is fixed by the
// insert/age-out rate, but batching into fewer, bigger DELETEs cuts statement +
// lock overhead. metricCleanupBatch is sized to clear the rows that accrue
// within the interval comfortably.
const (
	metricCleanupInterval = 2 * time.Minute
	metricCleanupBatch    = 2000
)

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
