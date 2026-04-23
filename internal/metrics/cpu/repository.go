package cpu

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"

	"system-stats/internal/app/dbutil"
)

type Repository interface {
	SaveCurrentMetric(ctx context.Context, metric CPUMetric, hostId uint) error
	GetLatestMetric(ctx context.Context) (CPUMetric, error)
	GetLatestMetricByHost(ctx context.Context, hostId uint) (*CPUMetric, error)
	GetHistoricalMetrics(ctx context.Context, hours float64) ([]HistoricalCPUMetric, error)
	GetHistoricalMetricsByHost(ctx context.Context, hostId uint, hours float64) ([]HistoricalCPUMetric, error)
}

type cpuRepository struct {
	db *gorm.DB
}

func NewRepository(db *gorm.DB) Repository {
	return &cpuRepository{db: db}
}

func cpuMetricFromHistorical(metric HistoricalCPUMetric) CPUMetric {
	return CPUMetric{
		UsagePercent: metric.Usage,
		Cores:        metric.Cores,
		LoadAvg1:     metric.LoadAvg1,
		LoadAvg5:     metric.LoadAvg5,
		LoadAvg15:    metric.LoadAvg15,
		Temperature:  metric.Temperature,
	}
}

func (r *cpuRepository) SaveCurrentMetric(ctx context.Context, metric CPUMetric, hostId uint) error {
	historicalMetric := HistoricalCPUMetric{
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

func (r *cpuRepository) GetLatestMetric(ctx context.Context) (CPUMetric, error) {
	var metric HistoricalCPUMetric

	err := r.db.WithContext(ctx).
		Order("timestamp DESC").
		First(&metric).Error
	if err != nil {
		return CPUMetric{}, err
	}

	return cpuMetricFromHistorical(metric), nil
}

func (r *cpuRepository) GetLatestMetricByHost(ctx context.Context, hostId uint) (*CPUMetric, error) {
	var metric HistoricalCPUMetric
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
	result := cpuMetricFromHistorical(metric)
	return &result, nil
}

func (r *cpuRepository) GetHistoricalMetrics(ctx context.Context, hours float64) ([]HistoricalCPUMetric, error) {
	var metrics []HistoricalCPUMetric
	err := dbutil.TimeOffsetQuery(r.db.WithContext(ctx), hours).
		Order("timestamp ASC").
		Find(&metrics).Error
	return metrics, err
}

func (r *cpuRepository) GetHistoricalMetricsByHost(ctx context.Context, hostId uint, hours float64) ([]HistoricalCPUMetric, error) {
	var metrics []HistoricalCPUMetric
	err := dbutil.TimeOffsetQueryWithHost(r.db.WithContext(ctx), hostId, hours).
		Order("timestamp ASC").
		Find(&metrics).Error
	return metrics, err
}
