package dockermetrics

import (
	"context"
	"errors"

	"system-stats/internal/modules/docker/domain/repositories"
	"system-stats/internal/modules/docker/infrastructure/entities"

	"github.com/charmbracelet/log"
)

type Service interface {
	Collect(ctx context.Context) (entities.DockerMetric, error)
	Save(ctx context.Context, metric entities.DockerMetric, hostId uint) error
	GetLatest(ctx context.Context) (entities.DockerMetric, error)
	GetLatestByHost(ctx context.Context, hostId uint) (*entities.DockerMetric, error)
	GetHistorical(ctx context.Context, hours float64) ([]repositories.HistoricalDockerMetric, error)
	GetHistoricalByHost(ctx context.Context, hostId uint, hours float64) ([]repositories.HistoricalDockerMetric, error)
	CollectAndSave(ctx context.Context, hostId uint) error
}

type service struct {
	logger           *log.Logger
	collector        repositories.DockerMetricsCollector
	dockerRepository repositories.DockerRepository
}

func NewService(logger *log.Logger, collector repositories.DockerMetricsCollector, dockerRepository repositories.DockerRepository) Service {
	return &service{
		logger:           logger,
		collector:        collector,
		dockerRepository: dockerRepository,
	}
}

func (s *service) Collect(ctx context.Context) (entities.DockerMetric, error) {
	s.logger.Debug("Collecting Docker metrics")
	metrics, err := s.collector.CollectDockerMetrics(ctx)
	if err != nil {
		s.logger.Error("Failed to collect Docker metrics", "error", err)
		return entities.DockerMetric{}, err
	}
	s.logger.Debug("Docker metrics collected", "stacks_count", len(metrics.Stacks), "total_containers", metrics.TotalContainers)
	return metrics, nil
}

func (s *service) Save(ctx context.Context, metric entities.DockerMetric, hostId uint) error {
	s.logger.Debug("Saving Docker metrics", "total_containers", metric.TotalContainers, "running_containers", metric.RunningContainers)
	err := s.dockerRepository.SaveCurrentMetric(ctx, metric, hostId)
	if err != nil {
		s.logger.Error("Failed to save Docker metrics", "error", err)
		return err
	}
	s.logger.Debug("Docker metrics saved")
	return nil
}

func (s *service) GetLatest(ctx context.Context) (entities.DockerMetric, error) {
	metric, err := s.dockerRepository.GetLatestMetric(ctx)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			s.logger.Debug("Context canceled while getting latest Docker metrics")
		} else {
			s.logger.Error("Failed to get latest Docker metrics", "error", err)
		}
		return entities.DockerMetric{}, err
	}
	return metric, nil
}

func (s *service) GetLatestByHost(ctx context.Context, hostId uint) (*entities.DockerMetric, error) {
	metric, err := s.dockerRepository.GetLatestMetricByHost(ctx, hostId)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			s.logger.Debug("Context canceled while getting latest Docker metrics by host")
		} else {
			s.logger.Error("Failed to get latest Docker metrics by host", "error", err, "host_id", hostId)
		}
		return nil, err
	}
	return metric, nil
}

func (s *service) GetHistorical(ctx context.Context, hours float64) ([]repositories.HistoricalDockerMetric, error) {
	metrics, err := s.dockerRepository.GetHistoricalMetrics(ctx, hours)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			s.logger.Debug("Context canceled while getting historical Docker metrics")
		} else {
			s.logger.Error("Failed to get historical Docker metrics", "error", err, "hours", hours)
		}
		return nil, err
	}
	return metrics, nil
}

func (s *service) GetHistoricalByHost(ctx context.Context, hostId uint, hours float64) ([]repositories.HistoricalDockerMetric, error) {
	metrics, err := s.dockerRepository.GetHistoricalMetricsByHost(ctx, hostId, hours)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			s.logger.Debug("Context canceled while getting historical Docker metrics by host")
		} else {
			s.logger.Error("Failed to get historical Docker metrics by host", "error", err, "host_id", hostId, "hours", hours)
		}
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
