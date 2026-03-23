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
	GetLatestByHost(ctx context.Context, hostId uint) (*entities.CPUMetric, error)
	GetHistorical(ctx context.Context, hours float64) ([]entities.HistoricalCPUMetric, error)
	GetHistoricalByHost(ctx context.Context, hostId uint, hours float64) ([]entities.HistoricalCPUMetric, error)
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
	s.logger.Debug("Collecting CPU metrics")
	metric, err := s.collector.CollectCPUMetrics(ctx)
	if err != nil {
		s.logger.Error("Failed to collect CPU metrics", "error", err)
		return entities.CPUMetric{}, err
	}
	s.logger.Debug("CPU metrics collected", "usage", metric.UsagePercent)
	return metric, nil
}

func (s *service) Save(ctx context.Context, metric entities.CPUMetric, hostId uint) error {
	s.logger.Debug("Saving CPU metrics", "usage", metric.UsagePercent, "host_id", hostId)
	err := s.cpuRepository.SaveCurrentMetric(ctx, metric, hostId)
	if err != nil {
		s.logger.Error("Failed to save CPU metrics", "error", err, "host_id", hostId)
		return err
	}
	s.logger.Debug("CPU metrics saved", "host_id", hostId)
	return nil
}

func (s *service) GetLatest(ctx context.Context) (entities.CPUMetric, error) {
	metric, err := s.cpuRepository.GetLatestMetric(ctx)
	if err != nil {
		s.logger.Error("Failed to get latest CPU metrics", "error", err)
		return entities.CPUMetric{}, err
	}
	return metric, nil
}

func (s *service) GetLatestByHost(ctx context.Context, hostId uint) (*entities.CPUMetric, error) {
	return s.cpuRepository.GetLatestMetricByHost(ctx, hostId)
}

func (s *service) GetHistorical(ctx context.Context, hours float64) ([]entities.HistoricalCPUMetric, error) {
	metrics, err := s.cpuRepository.GetHistoricalMetrics(ctx, hours)
	if err != nil {
		s.logger.Error("Failed to get historical CPU metrics", "error", err, "hours", hours)
		return nil, err
	}
	return metrics, nil
}

func (s *service) GetHistoricalByHost(ctx context.Context, hostId uint, hours float64) ([]entities.HistoricalCPUMetric, error) {
	metrics, err := s.cpuRepository.GetHistoricalMetricsByHost(ctx, hostId, hours)
	if err != nil {
		s.logger.Error("Failed to get historical CPU metrics by host", "error", err, "host_id", hostId, "hours", hours)
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
