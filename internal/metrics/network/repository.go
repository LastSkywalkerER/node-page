package network

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"

	"system-stats/internal/app/dbutil"
)

type Repository interface {
	SaveCurrentMetric(ctx context.Context, metric NetworkMetric, hostId uint) error
	GetLatestMetric(ctx context.Context) (NetworkMetric, error)
	GetLatestMetricByHost(ctx context.Context, hostId uint) (*NetworkMetric, error)
	GetHistoricalMetrics(ctx context.Context, hours float64) ([]NetworkMetric, error)
	GetHistoricalMetricsByHost(ctx context.Context, hostId uint, hours float64) ([]NetworkMetric, error)
}

type networkRepository struct {
	db *gorm.DB
}

func NewRepository(db *gorm.DB) Repository {
	return &networkRepository{db: db}
}

func (r *networkRepository) SaveCurrentMetric(ctx context.Context, metric NetworkMetric, hostId uint) error {
	historicalMetric := HistoricalNetworkMetric{
		HostID:     &hostId,
		Timestamp:  time.Now().UTC(),
		Interfaces: metric.Interfaces,
	}
	return r.db.WithContext(ctx).Create(&historicalMetric).Error
}

func (r *networkRepository) GetLatestMetric(ctx context.Context) (NetworkMetric, error) {
	var metric HistoricalNetworkMetric

	err := r.db.WithContext(ctx).
		Order("timestamp DESC").
		First(&metric).Error
	if err != nil {
		return NetworkMetric{}, err
	}

	return NetworkMetric{
		Interfaces: metric.Interfaces,
	}, nil
}

func (r *networkRepository) GetLatestMetricByHost(ctx context.Context, hostId uint) (*NetworkMetric, error) {
	var metric HistoricalNetworkMetric
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
	return &NetworkMetric{Interfaces: metric.Interfaces}, nil
}

func (r *networkRepository) GetHistoricalMetrics(ctx context.Context, hours float64) ([]NetworkMetric, error) {
	var historicalMetrics []HistoricalNetworkMetric

	err := dbutil.TimeOffsetQuery(r.db.WithContext(ctx), hours).
		Order("timestamp ASC").
		Find(&historicalMetrics).Error
	if err != nil {
		return nil, err
	}

	metrics := make([]NetworkMetric, len(historicalMetrics))
	for i, h := range historicalMetrics {
		metrics[i] = NetworkMetric{Interfaces: h.Interfaces}
	}
	return metrics, nil
}

func (r *networkRepository) GetHistoricalMetricsByHost(ctx context.Context, hostId uint, hours float64) ([]NetworkMetric, error) {
	var historicalMetrics []HistoricalNetworkMetric

	err := dbutil.TimeOffsetQueryWithHost(r.db.WithContext(ctx), hostId, hours).
		Order("timestamp ASC").
		Find(&historicalMetrics).Error
	if err != nil {
		return nil, err
	}

	metrics := make([]NetworkMetric, len(historicalMetrics))
	for i, h := range historicalMetrics {
		metrics[i] = NetworkMetric{Interfaces: h.Interfaces}
	}
	return metrics, nil
}
