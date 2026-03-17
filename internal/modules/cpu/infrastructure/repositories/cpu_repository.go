package repositories

import (
	"context"
	"time"

	"gorm.io/gorm"

	"system-stats/internal/app/database"
	localentities "system-stats/internal/modules/cpu/infrastructure/entities"
)

type CPURepository interface {
	SaveCurrentMetric(ctx context.Context, metric localentities.CPUMetric, hostId uint) error
	GetLatestMetric(ctx context.Context) (localentities.CPUMetric, error)
	GetHistoricalMetrics(ctx context.Context, hours float64) ([]localentities.HistoricalCPUMetric, error)
	GetHistoricalMetricsByHost(ctx context.Context, hostId uint, hours float64) ([]localentities.HistoricalCPUMetric, error)
}

type cpuRepository struct {
	db *gorm.DB
}

func NewCPURepository(db *gorm.DB) CPURepository {
	return &cpuRepository{db: db}
}

func (r *cpuRepository) SaveCurrentMetric(ctx context.Context, metric localentities.CPUMetric, hostId uint) error {
	historicalMetric := localentities.HistoricalCPUMetric{
		HostID:      &hostId,
		Timestamp:   time.Now().UTC(),
		Usage:       metric.UsagePercent,
		Cores:       metric.Cores,
		LoadAvg1:    metric.LoadAvg1,
		LoadAvg5:    metric.LoadAvg5,
		LoadAvg15:   metric.LoadAvg15,
		Temperature: metric.Temperature,
	}
	return r.db.WithContext(ctx).Create(&historicalMetric).Error
}

func (r *cpuRepository) GetLatestMetric(ctx context.Context) (localentities.CPUMetric, error) {
	var metric localentities.HistoricalCPUMetric

	err := r.db.WithContext(ctx).
		Order("timestamp DESC").
		First(&metric).Error
	if err != nil {
		return localentities.CPUMetric{}, err
	}

	return localentities.CPUMetric{
		UsagePercent: metric.Usage,
		Cores:        metric.Cores,
		LoadAvg1:     metric.LoadAvg1,
		LoadAvg5:     metric.LoadAvg5,
		LoadAvg15:    metric.LoadAvg15,
		Temperature:  metric.Temperature,
	}, nil
}

func (r *cpuRepository) GetHistoricalMetrics(ctx context.Context, hours float64) ([]localentities.HistoricalCPUMetric, error) {
	var metrics []localentities.HistoricalCPUMetric
	err := database.TimeOffsetQuery(r.db.WithContext(ctx), hours).
		Order("timestamp ASC").
		Find(&metrics).Error
	return metrics, err
}

func (r *cpuRepository) GetHistoricalMetricsByHost(ctx context.Context, hostId uint, hours float64) ([]localentities.HistoricalCPUMetric, error) {
	var metrics []localentities.HistoricalCPUMetric
	err := database.TimeOffsetQueryWithHost(r.db.WithContext(ctx), hostId, hours).
		Order("timestamp ASC").
		Find(&metrics).Error
	return metrics, err
}
