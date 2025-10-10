package diskmetrics

import (
	"context"

	"system-stats/internal/modules/disk/infrastructure/collectors"
	"system-stats/internal/modules/disk/infrastructure/entities"
	diskrepos "system-stats/internal/modules/disk/infrastructure/repositories"

	"github.com/charmbracelet/log"
)

type Service interface {
	Collect(ctx context.Context) (entities.DiskMetric, error)
	Save(ctx context.Context, metric entities.DiskMetric, hostId uint) error
	GetLatest(ctx context.Context) (entities.DiskMetric, error)
	GetHistorical(ctx context.Context, hours float64) ([]interface{}, error)
	GetHistoricalByHost(ctx context.Context, hostId uint, hours float64) ([]interface{}, error)
	CollectAndSave(ctx context.Context, hostId uint) error
}

type service struct {
	logger         *log.Logger
	collector      *collectors.DiskMetricsCollector
	diskRepository diskrepos.DiskRepository
}

func NewService(logger *log.Logger, diskRepository diskrepos.DiskRepository) Service {
	return &service{
		logger:         logger,
		collector:      collectors.NewDiskMetricsCollector(logger),
		diskRepository: diskRepository,
	}
}

func (s *service) Collect(ctx context.Context) (entities.DiskMetric, error) {
	s.logger.Info("Collecting disk metrics")
	metric, err := s.collector.CollectDiskMetrics(ctx)
	if err != nil {
		s.logger.Error("Failed to collect disk metrics", "error", err)
		return entities.DiskMetric{}, err
	}
	s.logger.Info("Disk metrics collected successfully", "usage_percent", metric.UsagePercent)
	return metric, nil
}

func (s *service) Save(ctx context.Context, metric entities.DiskMetric, hostId uint) error {
	s.logger.Info("Saving disk metrics to repository", "usage_percent", metric.UsagePercent, "host_id", hostId)
	err := s.diskRepository.SaveCurrentMetric(ctx, metric, hostId)
	if err != nil {
		s.logger.Error("Failed to save disk metrics", "error", err, "host_id", hostId)
		return err
	}
	s.logger.Info("Disk metrics saved successfully", "host_id", hostId)
	return nil
}

func (s *service) GetLatest(ctx context.Context) (entities.DiskMetric, error) {
	s.logger.Info("Getting latest disk metrics")
	metric, err := s.diskRepository.GetLatestMetric(ctx)
	if err != nil {
		s.logger.Error("Failed to get latest disk metrics", "error", err)
		return entities.DiskMetric{}, err
	}
	s.logger.Info("Latest disk metrics retrieved successfully")
	return metric, nil
}

func (s *service) GetHistorical(ctx context.Context, hours float64) ([]interface{}, error) {
	s.logger.Info("Getting historical disk metrics", "hours", hours)
	metrics, err := s.diskRepository.GetHistoricalMetrics(ctx, hours)
	if err != nil {
		s.logger.Error("Failed to get historical disk metrics", "error", err, "hours", hours)
		return nil, err
	}
	s.logger.Info("Historical disk metrics retrieved successfully", "count", len(metrics))

	// Convert to []interface{} for compatibility
	result := make([]interface{}, len(metrics))
	for i, metric := range metrics {
		result[i] = metric
	}
	return result, nil
}

func (s *service) GetHistoricalByHost(ctx context.Context, hostId uint, hours float64) ([]interface{}, error) {
	s.logger.Info("Getting historical disk metrics by host", "host_id", hostId, "hours", hours)
	metrics, err := s.diskRepository.GetHistoricalMetricsByHost(ctx, hostId, hours)
	if err != nil {
		s.logger.Error("Failed to get historical disk metrics by host", "error", err, "host_id", hostId, "hours", hours)
		return nil, err
	}
	s.logger.Info("Historical disk metrics by host retrieved successfully", "host_id", hostId, "count", len(metrics))

	// Convert to []interface{} for compatibility
	result := make([]interface{}, len(metrics))
	for i, metric := range metrics {
		result[i] = metric
	}
	return result, nil
}

func (s *service) CollectAndSave(ctx context.Context, hostId uint) error {
	metric, err := s.Collect(ctx)
	if err != nil {
		return err
	}
	return s.Save(ctx, metric, hostId)
}
