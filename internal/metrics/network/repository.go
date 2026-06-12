package network

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"system-stats/internal/app/dbutil"
)

type Repository interface {
	SaveCurrentMetric(ctx context.Context, metric NetworkMetric, hostId uint) error
	// SaveCurrentMetricAt persists with an explicit timestamp (Raft applier).
	SaveCurrentMetricAt(ctx context.Context, metric NetworkMetric, hostId uint, ts time.Time) error
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
	return r.SaveCurrentMetricAt(ctx, metric, hostId, time.Now().UTC())
}

func (r *networkRepository) SaveCurrentMetricAt(ctx context.Context, metric NetworkMetric, hostId uint, ts time.Time) error {
	historicalMetric := HistoricalNetworkMetric{
		HostID:     &hostId,
		Timestamp:  ts.UTC(),
		Interfaces: metric.Interfaces,
	}
	// Raft log replay re-applies entries after a restart: the same
	// (host_id, timestamp) row must be a no-op, not a pkey violation.
	return r.db.WithContext(ctx).
		Clauses(clause.OnConflict{DoNothing: true}).
		Create(&historicalMetric).Error
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
