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
	Save(ctx context.Context, metric entities.MemoryMetric, hostId uint) error
	GetLatest(ctx context.Context) (entities.MemoryMetric, error)
	GetHistorical(ctx context.Context, hours float64) ([]entities.HistoricalMemoryMetric, error)
	GetHistoricalByHost(ctx context.Context, hostId uint, hours float64) ([]entities.HistoricalMemoryMetric, error)
	CollectAndSave(ctx context.Context, hostId uint) error
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
	s.logger.Debug("Collecting memory metrics")
	metric, err := s.collector.CollectMemoryMetrics(ctx)
	if err != nil {
		s.logger.Error("Failed to collect memory metrics", "error", err)
		return entities.MemoryMetric{}, err
	}
	s.logger.Debug("Memory metrics collected", "usage_percent", metric.UsagePercent)
	return metric, nil
}

func (s *service) Save(ctx context.Context, metric entities.MemoryMetric, hostId uint) error {
	s.logger.Debug("Saving memory metrics", "usage_percent", metric.UsagePercent, "host_id", hostId)
	err := s.memoryRepository.SaveCurrentMetric(ctx, metric, hostId)
	if err != nil {
		s.logger.Error("Failed to save memory metrics", "error", err, "host_id", hostId)
		return err
	}
	s.logger.Debug("Memory metrics saved", "host_id", hostId)
	return nil
}

func (s *service) GetLatest(ctx context.Context) (entities.MemoryMetric, error) {
	// Collect fresh metrics to ensure all current fields are populated.
	metric, err := s.collector.CollectMemoryMetrics(ctx)
	if err != nil {
		s.logger.Error("Failed to collect latest memory metrics", "error", err)
		return entities.MemoryMetric{}, err
	}
	return metric, nil
}

func (s *service) GetHistorical(ctx context.Context, hours float64) ([]entities.HistoricalMemoryMetric, error) {
	metrics, err := s.memoryRepository.GetHistoricalMetrics(ctx, hours)
	if err != nil {
		s.logger.Error("Failed to get historical memory metrics", "error", err, "hours", hours)
		return nil, err
	}
	return metrics, nil
}

func (s *service) GetHistoricalByHost(ctx context.Context, hostId uint, hours float64) ([]entities.HistoricalMemoryMetric, error) {
	metrics, err := s.memoryRepository.GetHistoricalMetricsByHost(ctx, hostId, hours)
	if err != nil {
		s.logger.Error("Failed to get historical memory metrics by host", "error", err, "host_id", hostId, "hours", hours)
		return nil, err
	}
	return metrics, nil
}

func (s *service) CollectAndSave(ctx context.Context, hostId uint) error {
	metric, err := s.Collect(ctx)
	if err != nil {
		return err
	}
	return s.Save(ctx, metric, hostId)
}
