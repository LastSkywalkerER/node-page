package rammetrics

import (
	"context"

	"system-stats/internal/modules/memory/infrastructure/collectors"
	"system-stats/internal/modules/memory/infrastructure/entities"
	memoryrepos "system-stats/internal/modules/memory/infrastructure/repositories"

	"github.com/charmbracelet/log"
)

type Service interface {
	Collect(ctx context.Context) (entities.MemoryMetric, error)
	Save(ctx context.Context, metric entities.MemoryMetric) error
	GetLatest(ctx context.Context) (entities.MemoryMetric, error)
	GetHistorical(ctx context.Context, hours float64) ([]interface{}, error)
	CollectAndSave(ctx context.Context) error
}

type service struct {
	logger           *log.Logger
	collector        *collectors.MemoryMetricsCollector
	memoryRepository memoryrepos.MemoryRepository
}

func NewService(logger *log.Logger, memoryRepository memoryrepos.MemoryRepository) Service {
	return &service{
		logger:           logger,
		collector:        collectors.NewMemoryMetricsCollector(logger),
		memoryRepository: memoryRepository,
	}
}

func (s *service) Collect(ctx context.Context) (entities.MemoryMetric, error) {
	s.logger.Info("Collecting memory metrics")
	metric, err := s.collector.CollectMemoryMetrics(ctx)
	if err != nil {
		s.logger.Error("Failed to collect memory metrics", "error", err)
		return entities.MemoryMetric{}, err
	}
	s.logger.Info("Memory metrics collected successfully", "usage_percent", metric.UsagePercent)
	return metric, nil
}

func (s *service) Save(ctx context.Context, metric entities.MemoryMetric) error {
	s.logger.Info("Saving memory metrics to repository", "usage_percent", metric.UsagePercent)
	err := s.memoryRepository.SaveCurrentMetric(ctx, metric)
	if err != nil {
		s.logger.Error("Failed to save memory metrics", "error", err)
		return err
	}
	s.logger.Info("Memory metrics saved successfully")
	return nil
}

func (s *service) GetLatest(ctx context.Context) (entities.MemoryMetric, error) {
	s.logger.Info("Getting latest memory metrics")
	metric, err := s.memoryRepository.GetLatestMetric(ctx)
	if err != nil {
		s.logger.Error("Failed to get latest memory metrics", "error", err)
		return entities.MemoryMetric{}, err
	}
	s.logger.Info("Latest memory metrics retrieved successfully")
	return metric, nil
}

func (s *service) GetHistorical(ctx context.Context, hours float64) ([]interface{}, error) {
	s.logger.Info("Getting historical memory metrics", "hours", hours)
	metrics, err := s.memoryRepository.GetHistoricalMetrics(ctx, hours)
	if err != nil {
		s.logger.Error("Failed to get historical memory metrics", "error", err, "hours", hours)
		return nil, err
	}
	s.logger.Info("Historical memory metrics retrieved successfully", "count", len(metrics))

	// Convert to []interface{} for compatibility
	result := make([]interface{}, len(metrics))
	for i, metric := range metrics {
		result[i] = metric
	}
	return result, nil
}

func (s *service) CollectAndSave(ctx context.Context) error {
	metric, err := s.Collect(ctx)
	if err != nil {
		return err
	}
	return s.Save(ctx, metric)
}
