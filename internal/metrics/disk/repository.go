package disk

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"system-stats/internal/app/dbutil"
)

type Repository interface {
	SaveCurrentMetric(ctx context.Context, metric DiskMetric, hostId uint) error
	// SaveCurrentMetricAt persists with an explicit timestamp (Raft applier).
	SaveCurrentMetricAt(ctx context.Context, metric DiskMetric, hostId uint, ts time.Time) error
	GetLatestMetric(ctx context.Context) (DiskMetric, error)
	GetLatestMetricByHost(ctx context.Context, hostId uint) (*DiskMetric, error)
	GetHistoricalMetrics(ctx context.Context, hours float64) ([]HistoricalDiskMetric, error)
	GetHistoricalMetricsByHost(ctx context.Context, hostId uint, hours float64) ([]HistoricalDiskMetric, error)
}

type diskRepository struct {
	db *gorm.DB
}

func NewRepository(db *gorm.DB) Repository {
	return &diskRepository{db: db}
}

func (r *diskRepository) SaveCurrentMetric(ctx context.Context, metric DiskMetric, hostId uint) error {
	return r.SaveCurrentMetricAt(ctx, metric, hostId, time.Now().UTC())
}

func (r *diskRepository) SaveCurrentMetricAt(ctx context.Context, metric DiskMetric, hostId uint, ts time.Time) error {
	historicalMetric := HistoricalDiskMetric{
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

func (r *diskRepository) GetLatestMetric(ctx context.Context) (DiskMetric, error) {
	var metric HistoricalDiskMetric

	err := r.db.WithContext(ctx).
		Order("timestamp DESC").
		First(&metric).Error
	if err != nil {
		return DiskMetric{}, err
	}

	return DiskMetric{
		Total:        metric.TotalBytes,
		Used:         metric.UsedBytes,
		Free:         metric.TotalBytes - metric.UsedBytes,
		UsagePercent: metric.UsagePercent,
	}, nil
}

func (r *diskRepository) GetLatestMetricByHost(ctx context.Context, hostId uint) (*DiskMetric, error) {
	var metric HistoricalDiskMetric
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
	return &DiskMetric{
		Total:        metric.TotalBytes,
		Used:         metric.UsedBytes,
		Free:         metric.TotalBytes - metric.UsedBytes,
		UsagePercent: metric.UsagePercent,
	}, nil
}

func (r *diskRepository) GetHistoricalMetrics(ctx context.Context, hours float64) ([]HistoricalDiskMetric, error) {
	var metrics []HistoricalDiskMetric
	err := dbutil.TimeOffsetQuery(r.db.WithContext(ctx), hours).
		Order("timestamp ASC").
		Find(&metrics).Error
	return metrics, err
}

func (r *diskRepository) GetHistoricalMetricsByHost(ctx context.Context, hostId uint, hours float64) ([]HistoricalDiskMetric, error) {
	var metrics []HistoricalDiskMetric
	err := dbutil.TimeOffsetQueryWithHost(r.db.WithContext(ctx), hostId, hours).
		Order("timestamp ASC").
		Find(&metrics).Error
	return metrics, err
}
