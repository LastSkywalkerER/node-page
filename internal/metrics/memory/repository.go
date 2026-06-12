package memory

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"system-stats/internal/app/dbutil"
)

type Repository interface {
	SaveCurrentMetric(ctx context.Context, metric MemoryMetric, hostId uint) error
	// SaveCurrentMetricAt persists with an explicit timestamp (Raft applier).
	SaveCurrentMetricAt(ctx context.Context, metric MemoryMetric, hostId uint, ts time.Time) error
	GetLatestMetric(ctx context.Context) (MemoryMetric, error)
	GetLatestMetricByHost(ctx context.Context, hostId uint) (*MemoryMetric, error)
	GetHistoricalMetrics(ctx context.Context, hours float64) ([]HistoricalMemoryMetric, error)
	GetHistoricalMetricsByHost(ctx context.Context, hostId uint, hours float64) ([]HistoricalMemoryMetric, error)
}

type memoryRepository struct {
	db *gorm.DB
}

func NewRepository(db *gorm.DB) Repository {
	return &memoryRepository{db: db}
}

func (r *memoryRepository) SaveCurrentMetric(ctx context.Context, metric MemoryMetric, hostId uint) error {
	return r.SaveCurrentMetricAt(ctx, metric, hostId, time.Now().UTC())
}

func (r *memoryRepository) SaveCurrentMetricAt(ctx context.Context, metric MemoryMetric, hostId uint, ts time.Time) error {
	historicalMetric := HistoricalMemoryMetric{
		HostID:       &hostId,
		Timestamp:    ts.UTC(),
		UsagePercent: metric.UsagePercent,
		UsedBytes:    metric.Used,
		TotalBytes:   metric.Total,
	}
	// Raft log replay re-applies entries after a restart: the same
	// (host_id, timestamp) row must be a no-op, not a pkey violation.
	return r.db.WithContext(ctx).
		Clauses(clause.OnConflict{DoNothing: true}).
		Create(&historicalMetric).Error
}

func (r *memoryRepository) GetLatestMetric(ctx context.Context) (MemoryMetric, error) {
	var metric HistoricalMemoryMetric

	err := r.db.WithContext(ctx).
		Order("timestamp DESC").
		First(&metric).Error
	if err != nil {
		return MemoryMetric{}, err
	}

	return MemoryMetric{
		Total:        metric.TotalBytes,
		Used:         metric.UsedBytes,
		UsagePercent: metric.UsagePercent,
		Available:    metric.TotalBytes - metric.UsedBytes,
		Free:         metric.TotalBytes - metric.UsedBytes,
	}, nil
}

func (r *memoryRepository) GetLatestMetricByHost(ctx context.Context, hostId uint) (*MemoryMetric, error) {
	var metric HistoricalMemoryMetric
	err := r.db.WithContext(ctx).
		Where("host_id = ?", hostId).
		Order("timestamp DESC").
		First(&metric).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &MemoryMetric{
		Total:        metric.TotalBytes,
		Used:         metric.UsedBytes,
		UsagePercent: metric.UsagePercent,
		Available:    metric.TotalBytes - metric.UsedBytes,
		Free:         metric.TotalBytes - metric.UsedBytes,
	}, nil
}

func (r *memoryRepository) GetHistoricalMetrics(ctx context.Context, hours float64) ([]HistoricalMemoryMetric, error) {
	var metrics []HistoricalMemoryMetric
	err := dbutil.TimeOffsetQuery(r.db.WithContext(ctx), hours).
		Order("timestamp ASC").
		Find(&metrics).Error
	return metrics, err
}

func (r *memoryRepository) GetHistoricalMetricsByHost(ctx context.Context, hostId uint, hours float64) ([]HistoricalMemoryMetric, error) {
	var metrics []HistoricalMemoryMetric
	err := dbutil.TimeOffsetQueryWithHost(r.db.WithContext(ctx), hostId, hours).
		Order("timestamp ASC").
		Find(&metrics).Error
	return metrics, err
}
