package repositories

import (
	"context"
	"time"

	"gorm.io/gorm"

	localentities "system-stats/internal/modules/memory/infrastructure/entities"
)

type MemoryRepository interface {
	SaveCurrentMetric(ctx context.Context, metric localentities.MemoryMetric) error
	GetLatestMetric(ctx context.Context) (localentities.MemoryMetric, error)
	GetHistoricalMetrics(ctx context.Context, hours float64) ([]localentities.HistoricalMemoryMetric, error)
}

type memoryRepository struct {
	db *gorm.DB
}

func NewMemoryRepository(db *gorm.DB) MemoryRepository {
	// Auto-migrate the historical memory metrics table
	db.AutoMigrate(&localentities.HistoricalMemoryMetric{})
	return &memoryRepository{db: db}
}

func (r *memoryRepository) SaveCurrentMetric(ctx context.Context, metric localentities.MemoryMetric) error {
	// Save as historical metric
	historicalMetric := localentities.HistoricalMemoryMetric{
		Timestamp:    time.Now().UTC(),
		UsagePercent: metric.UsagePercent,
		UsedBytes:    metric.Used,
		TotalBytes:   metric.Total,
	}

	return r.db.WithContext(ctx).Create(&historicalMetric).Error
}

func (r *memoryRepository) GetLatestMetric(ctx context.Context) (localentities.MemoryMetric, error) {
	var metric localentities.HistoricalMemoryMetric

	err := r.db.WithContext(ctx).
		Order("timestamp DESC").
		First(&metric).Error

	if err != nil {
		return localentities.MemoryMetric{}, err
	}

	// Convert historical to current metric
	return localentities.MemoryMetric{
		Total:        metric.TotalBytes,
		Used:         metric.UsedBytes,
		UsagePercent: metric.UsagePercent,
		Available:    metric.TotalBytes - metric.UsedBytes,
		Free:         metric.TotalBytes - metric.UsedBytes,
	}, nil
}

func (r *memoryRepository) GetHistoricalMetrics(ctx context.Context, hours float64) ([]localentities.HistoricalMemoryMetric, error) {
	var metrics []localentities.HistoricalMemoryMetric

	query := r.db.WithContext(ctx).
		Where("timestamp >= datetime('now', '-' || ? || ' hours')", hours).
		Order("timestamp ASC").
		Find(&metrics)

	return metrics, query.Error
}
