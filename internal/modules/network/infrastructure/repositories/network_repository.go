package repositories

import (
	"context"
	"time"

	"gorm.io/gorm"

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
	// Auto-migrate the historical network metrics table
	db.AutoMigrate(&localentities.HistoricalNetworkMetric{})
	return &networkRepository{db: db}
}

func (r *networkRepository) SaveCurrentMetric(ctx context.Context, metric localentities.NetworkMetric, hostId uint) error {
	// Save complete network metric as historical record
	historicalMetric := localentities.HistoricalNetworkMetric{
		HostID:     &hostId,
		Timestamp:  time.Now().UTC(),
		Interfaces: metric.Interfaces, // Store complete interfaces array
	}

	if err := r.db.WithContext(ctx).Create(&historicalMetric).Error; err != nil {
		return err
	}

	return nil
}

func (r *networkRepository) GetLatestMetric(ctx context.Context) (localentities.NetworkMetric, error) {
	var metric localentities.HistoricalNetworkMetric

	err := r.db.WithContext(ctx).
		Order("timestamp DESC").
		First(&metric).Error

	if err != nil {
		return localentities.NetworkMetric{}, err
	}

	// Convert historical to current metric format
	return localentities.NetworkMetric{
		Interfaces: metric.Interfaces,
	}, nil
}

func (r *networkRepository) GetHistoricalMetrics(ctx context.Context, hours float64) ([]localentities.NetworkMetric, error) {
	var historicalMetrics []localentities.HistoricalNetworkMetric

	query := r.db.WithContext(ctx).
		Where("timestamp >= datetime('now', '-' || ? || ' hours')", hours).
		Order("timestamp ASC").
		Find(&historicalMetrics)

	if query.Error != nil {
		return nil, query.Error
	}

	// Convert to NetworkMetric format
	metrics := make([]localentities.NetworkMetric, len(historicalMetrics))
	for i, historical := range historicalMetrics {
		metrics[i] = localentities.NetworkMetric{
			Interfaces: historical.Interfaces,
		}
	}

	// Debug log
	if len(metrics) > 0 {
		println("DEBUG: Historical metrics retrieved, first metric has", len(metrics[0].Interfaces), "interfaces")
	}

	return metrics, nil
}

func (r *networkRepository) GetHistoricalMetricsByHost(ctx context.Context, hostId uint, hours float64) ([]localentities.NetworkMetric, error) {
	var historicalMetrics []localentities.HistoricalNetworkMetric

	query := r.db.WithContext(ctx).
		Where("host_id = ? AND timestamp >= datetime('now', '-' || ? || ' hours')", hostId, hours).
		Order("timestamp ASC").
		Find(&historicalMetrics)

	if query.Error != nil {
		return nil, query.Error
	}

	// Convert to NetworkMetric format
	metrics := make([]localentities.NetworkMetric, len(historicalMetrics))
	for i, historical := range historicalMetrics {
		metrics[i] = localentities.NetworkMetric{
			Interfaces: historical.Interfaces,
		}
	}

	return metrics, nil
}
