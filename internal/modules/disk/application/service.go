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
	GetLatestByHost(ctx context.Context, hostId uint) (*entities.DiskMetric, error)
	GetHistorical(ctx context.Context, hours float64) ([]entities.HistoricalDiskMetric, error)
	GetHistoricalByHost(ctx context.Context, hostId uint, hours float64) ([]entities.HistoricalDiskMetric, error)
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
	s.logger.Debug("Collecting disk metrics")
	metric, err := s.collector.CollectDiskMetrics(ctx)
	if err != nil {
		s.logger.Error("Failed to collect disk metrics", "error", err)
		return entities.DiskMetric{}, err
	}
	s.logger.Debug("Disk metrics collected", "usage_percent", metric.UsagePercent)
	return metric, nil
}

func (s *service) Save(ctx context.Context, metric entities.DiskMetric, hostId uint) error {
	s.logger.Debug("Saving disk metrics", "usage_percent", metric.UsagePercent, "host_id", hostId)
	err := s.diskRepository.SaveCurrentMetric(ctx, metric, hostId)
	if err != nil {
		s.logger.Error("Failed to save disk metrics", "error", err, "host_id", hostId)
		return err
	}
	s.logger.Debug("Disk metrics saved", "host_id", hostId)
	return nil
}

func (s *service) GetLatest(ctx context.Context) (entities.DiskMetric, error) {
	metric, err := s.diskRepository.GetLatestMetric(ctx)
	if err != nil {
		s.logger.Error("Failed to get latest disk metrics", "error", err)
		return entities.DiskMetric{}, err
	}
	return metric, nil
}

func (s *service) GetLatestByHost(ctx context.Context, hostId uint) (*entities.DiskMetric, error) {
	return s.diskRepository.GetLatestMetricByHost(ctx, hostId)
}

func (s *service) GetHistorical(ctx context.Context, hours float64) ([]entities.HistoricalDiskMetric, error) {
	metrics, err := s.diskRepository.GetHistoricalMetrics(ctx, hours)
	if err != nil {
		s.logger.Error("Failed to get historical disk metrics", "error", err, "hours", hours)
		return nil, err
	}
	return metrics, nil
}

func (s *service) GetHistoricalByHost(ctx context.Context, hostId uint, hours float64) ([]entities.HistoricalDiskMetric, error) {
	metrics, err := s.diskRepository.GetHistoricalMetricsByHost(ctx, hostId, hours)
	if err != nil {
		s.logger.Error("Failed to get historical disk metrics by host", "error", err, "host_id", hostId, "hours", hours)
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
