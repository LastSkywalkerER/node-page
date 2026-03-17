package repositories

import (
	"context"
	"time"

	"gorm.io/gorm"

	"system-stats/internal/app/database"
	localentities "system-stats/internal/modules/disk/infrastructure/entities"
)

type DiskRepository interface {
	SaveCurrentMetric(ctx context.Context, metric localentities.DiskMetric, hostId uint) error
	GetLatestMetric(ctx context.Context) (localentities.DiskMetric, error)
	GetHistoricalMetrics(ctx context.Context, hours float64) ([]localentities.HistoricalDiskMetric, error)
	GetHistoricalMetricsByHost(ctx context.Context, hostId uint, hours float64) ([]localentities.HistoricalDiskMetric, error)
}

type diskRepository struct {
	db *gorm.DB
}

func NewDiskRepository(db *gorm.DB) DiskRepository {
	return &diskRepository{db: db}
}

func (r *diskRepository) SaveCurrentMetric(ctx context.Context, metric localentities.DiskMetric, hostId uint) error {
	historicalMetric := localentities.HistoricalDiskMetric{
		HostID:       &hostId,
		Timestamp:    time.Now().UTC(),
		UsagePercent: metric.UsagePercent,
		UsedBytes:    metric.Used,
		TotalBytes:   metric.Total,
	}
	return r.db.WithContext(ctx).Create(&historicalMetric).Error
}

func (r *diskRepository) GetLatestMetric(ctx context.Context) (localentities.DiskMetric, error) {
	var metric localentities.HistoricalDiskMetric

	err := r.db.WithContext(ctx).
		Order("timestamp DESC").
		First(&metric).Error
	if err != nil {
		return localentities.DiskMetric{}, err
	}

	return localentities.DiskMetric{
		Total:        metric.TotalBytes,
		Used:         metric.UsedBytes,
		Free:         metric.TotalBytes - metric.UsedBytes,
		UsagePercent: metric.UsagePercent,
	}, nil
}

func (r *diskRepository) GetHistoricalMetrics(ctx context.Context, hours float64) ([]localentities.HistoricalDiskMetric, error) {
	var metrics []localentities.HistoricalDiskMetric
	err := database.TimeOffsetQuery(r.db.WithContext(ctx), hours).
		Order("timestamp ASC").
		Find(&metrics).Error
	return metrics, err
}

func (r *diskRepository) GetHistoricalMetricsByHost(ctx context.Context, hostId uint, hours float64) ([]localentities.HistoricalDiskMetric, error) {
	var metrics []localentities.HistoricalDiskMetric
	err := database.TimeOffsetQueryWithHost(r.db.WithContext(ctx), hostId, hours).
		Order("timestamp ASC").
		Find(&metrics).Error
	return metrics, err
}
