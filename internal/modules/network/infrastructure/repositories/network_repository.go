package repositories

import (
	"context"
	"time"

	"gorm.io/gorm"

	"system-stats/internal/app/database"
	localentities "system-stats/internal/modules/network/infrastructure/entities"
)

type NetworkRepository interface {
	SaveCurrentMetric(ctx context.Context, metric localentities.NetworkMetric, hostId uint) error
	GetLatestMetric(ctx context.Context) (localentities.NetworkMetric, error)
	GetHistoricalMetrics(ctx context.Context, hours float64) ([]localentities.NetworkMetric, error)
	GetHistoricalMetricsByHost(ctx context.Context, hostId uint, hours float64) ([]localentities.NetworkMetric, error)
}

type networkRepository struct {
	db *gorm.DB
}

func NewNetworkRepository(db *gorm.DB) NetworkRepository {
	return &networkRepository{db: db}
}

func (r *networkRepository) SaveCurrentMetric(ctx context.Context, metric localentities.NetworkMetric, hostId uint) error {
	historicalMetric := localentities.HistoricalNetworkMetric{
		HostID:     &hostId,
		Timestamp:  time.Now().UTC(),
		Interfaces: metric.Interfaces,
	}
	return r.db.WithContext(ctx).Create(&historicalMetric).Error
}

func (r *networkRepository) GetLatestMetric(ctx context.Context) (localentities.NetworkMetric, error) {
	var metric localentities.HistoricalNetworkMetric

	err := r.db.WithContext(ctx).
		Order("timestamp DESC").
		First(&metric).Error
	if err != nil {
		return localentities.NetworkMetric{}, err
	}

	return localentities.NetworkMetric{
		Interfaces: metric.Interfaces,
	}, nil
}

func (r *networkRepository) GetHistoricalMetrics(ctx context.Context, hours float64) ([]localentities.NetworkMetric, error) {
	var historicalMetrics []localentities.HistoricalNetworkMetric

	err := database.TimeOffsetQuery(r.db.WithContext(ctx), hours).
		Order("timestamp ASC").
		Find(&historicalMetrics).Error
	if err != nil {
		return nil, err
	}

	metrics := make([]localentities.NetworkMetric, len(historicalMetrics))
	for i, h := range historicalMetrics {
		metrics[i] = localentities.NetworkMetric{Interfaces: h.Interfaces}
	}
	return metrics, nil
}

func (r *networkRepository) GetHistoricalMetricsByHost(ctx context.Context, hostId uint, hours float64) ([]localentities.NetworkMetric, error) {
	var historicalMetrics []localentities.HistoricalNetworkMetric

	err := database.TimeOffsetQueryWithHost(r.db.WithContext(ctx), hostId, hours).
		Order("timestamp ASC").
		Find(&historicalMetrics).Error
	if err != nil {
		return nil, err
	}

	metrics := make([]localentities.NetworkMetric, len(historicalMetrics))
	for i, h := range historicalMetrics {
		metrics[i] = localentities.NetworkMetric{Interfaces: h.Interfaces}
	}
	return metrics, nil
}
