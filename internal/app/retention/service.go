package retention

import (
	"context"
	"time"

	"github.com/charmbracelet/log"
	"gorm.io/gorm"
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
// driven by the periodic metrics collection hook (no internal ticker).
type Service struct {
	db            *gorm.DB
	logger        *log.Logger
	retentionDays int
}

func NewService(db *gorm.DB, logger *log.Logger, retentionDays int) *Service {
	return &Service{db: db, logger: logger, retentionDays: retentionDays}
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

	// docker_container_entities is the heaviest history table (hundreds of
	// thousands of rows / hundreds of MB) and was previously uncovered by
	// retention. It has no host_id column, so it is keyed off metric_timestamp
	// (now indexed) with the same batch shape as the metric tables above.
	if ctx.Err() == nil {
		const containerTable = "docker_container_entities"
		cq := "DELETE FROM " + containerTable + " WHERE rowid IN (SELECT rowid FROM " + containerTable + " WHERE metric_timestamp < ? LIMIT ?)"
		if s.db.Dialector.Name() == "postgres" {
			cq = "DELETE FROM " + containerTable + " WHERE ctid IN (SELECT ctid FROM " + containerTable + " WHERE metric_timestamp < ? LIMIT ?)"
		}
		res := s.db.WithContext(ctx).Exec(cq, cutoff, batchSize)
		if res.Error != nil {
			s.logger.Error("Retention batch failed", "table", containerTable, "error", res.Error)
		} else if res.RowsAffected > 0 {
			totalDeleted += res.RowsAffected
			s.logger.Debug("Retention batch", "table", containerTable, "deleted", res.RowsAffected)
		}
	}

	return totalDeleted, nil
}

func (s *Service) cutoffTime() interface{} {
	// Use database-native NOW() arithmetic via raw SQL through cutoff parameter.
	// Keeping it as a Go time keeps things consistent across dialects.
	return timeNowMinusDays(s.retentionDays)
}
