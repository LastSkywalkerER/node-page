package docker

import (
	"context"
	"errors"

	"github.com/charmbracelet/log"
)

// Service defines the Docker metrics service interface.
type Service interface {
	Collect(ctx context.Context) (DockerMetric, error)
	Save(ctx context.Context, metric DockerMetric, hostId uint) error
	GetLatest(ctx context.Context) (DockerMetric, error)
	GetLatestByHost(ctx context.Context, hostId uint) (*DockerMetric, error)
	GetHistorical(ctx context.Context, hours float64) ([]HistoricalDockerMetric, error)
	GetHistoricalByHost(ctx context.Context, hostId uint, hours float64) ([]HistoricalDockerMetric, error)
	CollectAndSave(ctx context.Context, hostId uint) error

	// GetApplicationsByHost groups a host's containers into applications (compose
	// project / swarm stack / standalone) — a read-time projection. Works for any
	// host whose docker snapshot is in the local DB (incl. Raft-replicated peers).
	GetApplicationsByHost(ctx context.Context, hostId uint) ([]DockerApplication, error)
	// GetApplication returns one application by project key for a host, or nil.
	GetApplication(ctx context.Context, hostId uint, project string) (*DockerApplication, error)
	// GetComposeView returns the synthetic compose (always) plus the real file(s)
	// when allowReal and the path is reachable from this process.
	GetComposeView(ctx context.Context, hostId uint, project string, allowReal bool) (*ComposeView, error)
	// GetContainerLogs returns recent logs for a container on the local daemon,
	// resolving the live container from the ref (handles recreated/stopped ones).
	GetContainerLogs(ctx context.Context, ref ContainerLogRef, tail int) (string, error)
}

type service struct {
	logger           *log.Logger
	collector        DockerMetricsCollector
	dockerRepository DockerRepository
}

// NewService creates a new Docker metrics service.
func NewService(logger *log.Logger, collector DockerMetricsCollector, repo DockerRepository) Service {
	return &service{
		logger:           logger,
		collector:        collector,
		dockerRepository: repo,
	}
}

func (s *service) Collect(ctx context.Context) (DockerMetric, error) {
	s.logger.Debug("Collecting Docker metrics")
	metrics, err := s.collector.CollectDockerMetrics(ctx)
	if err != nil {
		s.logger.Error("Failed to collect Docker metrics", "error", err)
		return DockerMetric{}, err
	}
	s.logger.Debug("Docker metrics collected", "stacks_count", len(metrics.Stacks), "total_containers", metrics.TotalContainers)
	return metrics, nil
}

func (s *service) Save(ctx context.Context, metric DockerMetric, hostId uint) error {
	s.logger.Debug("Saving Docker metrics", "total_containers", metric.TotalContainers, "running_containers", metric.RunningContainers)
	err := s.dockerRepository.SaveCurrentMetric(ctx, metric, hostId)
	if err != nil {
		s.logger.Error("Failed to save Docker metrics", "error", err)
		return err
	}
	s.logger.Debug("Docker metrics saved")
	return nil
}

func (s *service) GetLatest(ctx context.Context) (DockerMetric, error) {
	metric, err := s.dockerRepository.GetLatestMetric(ctx)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			s.logger.Debug("Context canceled while getting latest Docker metrics")
		} else {
			s.logger.Error("Failed to get latest Docker metrics", "error", err)
		}
		return DockerMetric{}, err
	}
	return metric, nil
}

func (s *service) GetLatestByHost(ctx context.Context, hostId uint) (*DockerMetric, error) {
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

func (s *service) GetHistorical(ctx context.Context, hours float64) ([]HistoricalDockerMetric, error) {
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

func (s *service) GetHistoricalByHost(ctx context.Context, hostId uint, hours float64) ([]HistoricalDockerMetric, error) {
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

func (s *service) GetApplicationsByHost(ctx context.Context, hostId uint) ([]DockerApplication, error) {
	metric, err := s.dockerRepository.GetLatestMetricByHost(ctx, hostId)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			s.logger.Debug("Context canceled while getting applications by host")
		} else {
			s.logger.Error("Failed to get applications by host", "error", err, "host_id", hostId)
		}
		return nil, err
	}
	return BuildApplications(metric), nil
}

func (s *service) GetApplication(ctx context.Context, hostId uint, project string) (*DockerApplication, error) {
	apps, err := s.GetApplicationsByHost(ctx, hostId)
	if err != nil {
		return nil, err
	}
	for i := range apps {
		if apps[i].Project == project {
			apps[i].HostID = hostId
			return &apps[i], nil
		}
	}
	return nil, nil
}

func (s *service) GetComposeView(ctx context.Context, hostId uint, project string, allowReal bool) (*ComposeView, error) {
	app, err := s.GetApplication(ctx, hostId, project)
	if err != nil {
		return nil, err
	}
	if app == nil {
		return nil, nil
	}
	view := &ComposeView{
		Project:   project,
		Synthetic: BuildSyntheticCompose(app),
	}
	if allowReal {
		content, files, available := ReadRealCompose(app)
		view.Real = content
		view.ConfigFiles = files
		view.RealAvailable = available
	}
	return view, nil
}

func (s *service) GetContainerLogs(ctx context.Context, ref ContainerLogRef, tail int) (string, error) {
	return s.collector.GetContainerLogs(ctx, ref, tail)
}
