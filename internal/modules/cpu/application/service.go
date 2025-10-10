package cpumetrics

import (
	"context"

	"system-stats/internal/modules/cpu/infrastructure/collectors"
	"system-stats/internal/modules/cpu/infrastructure/entities"
	cpurepos "system-stats/internal/modules/cpu/infrastructure/repositories"

	"github.com/charmbracelet/log"
)

type Service interface {
	Collect(ctx context.Context) (entities.CPUMetric, error)
	Save(ctx context.Context, metric entities.CPUMetric, hostId uint) error
	GetLatest(ctx context.Context) (entities.CPUMetric, error)
	GetHistorical(ctx context.Context, hours float64) ([]interface{}, error)
	GetHistoricalByHost(ctx context.Context, hostId uint, hours float64) ([]interface{}, error)
	CollectAndSave(ctx context.Context, hostId uint) error
}

type service struct {
	logger        *log.Logger
	collector     *collectors.CPUMetricsCollector
	cpuRepository cpurepos.CPURepository
}

func NewService(logger *log.Logger, cpuRepository cpurepos.CPURepository) Service {
	return &service{
		logger:        logger,
		collector:     collectors.NewCPUMetricsCollector(logger),
		cpuRepository: cpuRepository,
	}
}

func (s *service) Collect(ctx context.Context) (entities.CPUMetric, error) {
	s.logger.Info("Collecting CPU metrics")
	metric, err := s.collector.CollectCPUMetrics(ctx)
	if err != nil {
		s.logger.Error("Failed to collect CPU metrics", "error", err)
		return entities.CPUMetric{}, err
	}
	s.logger.Info("CPU metrics collected successfully", "usage", metric.UsagePercent)
	return metric, nil
}

func (s *service) Save(ctx context.Context, metric entities.CPUMetric, hostId uint) error {
	s.logger.Info("Saving CPU metrics to repository", "usage", metric.UsagePercent, "host_id", hostId)
	err := s.cpuRepository.SaveCurrentMetric(ctx, metric, hostId)
	if err != nil {
		s.logger.Error("Failed to save CPU metrics", "error", err, "host_id", hostId)
		return err
	}
	s.logger.Info("CPU metrics saved successfully", "host_id", hostId)
	return nil
}

func (s *service) GetLatest(ctx context.Context) (entities.CPUMetric, error) {
	s.logger.Info("Getting latest CPU metrics")
	metric, err := s.cpuRepository.GetLatestMetric(ctx)
	if err != nil {
		s.logger.Error("Failed to get latest CPU metrics", "error", err)
		return entities.CPUMetric{}, err
	}
	s.logger.Info("Latest CPU metrics retrieved successfully")
	return metric, nil
}

func (s *service) GetHistorical(ctx context.Context, hours float64) ([]interface{}, error) {
	s.logger.Info("Getting historical CPU metrics", "hours", hours)
	metrics, err := s.cpuRepository.GetHistoricalMetrics(ctx, hours)
	if err != nil {
		s.logger.Error("Failed to get historical CPU metrics", "error", err, "hours", hours)
		return nil, err
	}
	s.logger.Info("Historical CPU metrics retrieved successfully", "count", len(metrics))

	// Convert to []interface{} for compatibility
	result := make([]interface{}, len(metrics))
	for i, metric := range metrics {
		result[i] = metric
	}
	return result, nil
}

func (s *service) GetHistoricalByHost(ctx context.Context, hostId uint, hours float64) ([]interface{}, error) {
	s.logger.Info("Getting historical CPU metrics by host", "host_id", hostId, "hours", hours)
	metrics, err := s.cpuRepository.GetHistoricalMetricsByHost(ctx, hostId, hours)
	if err != nil {
		s.logger.Error("Failed to get historical CPU metrics by host", "error", err, "host_id", hostId, "hours", hours)
		return nil, err
	}
	s.logger.Info("Historical CPU metrics by host retrieved successfully", "host_id", hostId, "count", len(metrics))

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
