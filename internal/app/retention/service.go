package retention

import (
	"context"
	"time"

	"github.com/charmbracelet/log"
	"gorm.io/gorm"
)

var MetricTables = []string{
	"cpu_metrics",
	"memory_metrics",
	"disk_metrics",
	"network_metrics",
	"docker_metrics",
}

// Service deletes metric rows older than RetentionDays on an hourly schedule.
type Service struct {
	db            *gorm.DB
	logger        *log.Logger
	retentionDays int
}

func NewService(db *gorm.DB, logger *log.Logger, retentionDays int) *Service {
	return &Service{db: db, logger: logger, retentionDays: retentionDays}
}

// Start runs an immediate cleanup then repeats every hour until ctx is cancelled.
func (s *Service) Start(ctx context.Context) {
	s.Cleanup()
	ticker := time.NewTicker(time.Hour)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				s.Cleanup()
			case <-ctx.Done():
				return
			}
		}
	}()
}

func (s *Service) Cleanup() {
	cutoff := time.Now().AddDate(0, 0, -s.retentionDays)
	for _, table := range MetricTables {
		result := s.db.Exec("DELETE FROM "+table+" WHERE timestamp < ?", cutoff)
		if result.Error != nil {
			s.logger.Error("Retention cleanup failed", "table", table, "error", result.Error)
		} else if result.RowsAffected > 0 {
			s.logger.Debug("Retention cleanup", "table", table, "deleted", result.RowsAffected)
		}
	}
}
