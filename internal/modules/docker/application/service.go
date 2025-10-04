package dockermetrics

import (
	"context"

	"system-stats/internal/modules/docker/domain/repositories"
	"system-stats/internal/modules/docker/infrastructure/entities"

	"github.com/charmbracelet/log"
)

type Service interface {
	Collect(ctx context.Context) (entities.DockerMetric, error)
	Save(ctx context.Context, metric entities.DockerMetric) error
	GetLatest(ctx context.Context) (entities.DockerMetric, error)
	GetHistorical(ctx context.Context, hours float64) ([]interface{}, error)
	CollectAndSave(ctx context.Context) error
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
	s.logger.Info("Collecting Docker metrics")
	metrics, err := s.collector.CollectDockerMetrics(ctx)
	if err != nil {
		s.logger.Error("Failed to collect Docker metrics", "error", err)
		return entities.DockerMetric{}, err
	}
	s.logger.Info("Docker metrics collected successfully", "stacks_count", len(metrics.Stacks), "total_containers", metrics.TotalContainers)
	return metrics, nil
}

func (s *service) Save(ctx context.Context, metric entities.DockerMetric) error {
	s.logger.Info("Saving Docker metrics to repository", "total_containers", metric.TotalContainers, "running_containers", metric.RunningContainers)
	err := s.dockerRepository.SaveCurrentMetric(ctx, metric)
	if err != nil {
		s.logger.Error("Failed to save Docker metrics", "error", err)
		return err
	}
	s.logger.Info("Docker metrics saved successfully")
	return nil
}

func (s *service) GetLatest(ctx context.Context) (entities.DockerMetric, error) {
	s.logger.Info("Getting latest Docker metrics")
	metric, err := s.dockerRepository.GetLatestMetric(ctx)
	if err != nil {
		s.logger.Error("Failed to get latest Docker metrics", "error", err)
		return entities.DockerMetric{}, err
	}
	s.logger.Info("Latest Docker metrics retrieved successfully")
	return metric, nil
}

func (s *service) GetHistorical(ctx context.Context, hours float64) ([]interface{}, error) {
	s.logger.Info("Getting historical Docker metrics", "hours", hours)
	metrics, err := s.dockerRepository.GetHistoricalMetrics(ctx, hours)
	if err != nil {
		s.logger.Error("Failed to get historical Docker metrics", "error", err, "hours", hours)
		return nil, err
	}
	s.logger.Info("Historical Docker metrics retrieved successfully", "count", len(metrics))

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
