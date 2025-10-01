package repositories

import (
	"context"
	"time"

	"gorm.io/gorm"

	localentities "system-stats/internal/modules/disk/infrastructure/entities"
)

type DiskRepository interface {
	SaveCurrentMetric(ctx context.Context, metric localentities.DiskMetric) error
	GetLatestMetric(ctx context.Context) (localentities.DiskMetric, error)
	GetHistoricalMetrics(ctx context.Context, hours float64) ([]localentities.HistoricalDiskMetric, error)
}

type diskRepository struct {
	db *gorm.DB
}

func NewDiskRepository(db *gorm.DB) DiskRepository {
	// Auto-migrate the historical disk metrics table
	db.AutoMigrate(&localentities.HistoricalDiskMetric{})
	return &diskRepository{db: db}
}

func (r *diskRepository) SaveCurrentMetric(ctx context.Context, metric localentities.DiskMetric) error {
	// Save as historical metric
	historicalMetric := localentities.HistoricalDiskMetric{
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

	// Convert historical to current metric
	return localentities.DiskMetric{
		Total:        metric.TotalBytes,
		Used:         metric.UsedBytes,
		Free:         metric.TotalBytes - metric.UsedBytes,
		UsagePercent: metric.UsagePercent,
	}, nil
}

func (r *diskRepository) GetHistoricalMetrics(ctx context.Context, hours float64) ([]localentities.HistoricalDiskMetric, error) {
	var metrics []localentities.HistoricalDiskMetric

	query := r.db.WithContext(ctx).
		Where("timestamp >= datetime('now', '-' || ? || ' hours')", hours).
		Order("timestamp ASC").
		Find(&metrics)

	return metrics, query.Error
}
