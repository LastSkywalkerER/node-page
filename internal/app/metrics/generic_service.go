package metrics

import (
	"context"

	"github.com/charmbracelet/log"
)

type Collector[M any] interface {
	Collect(ctx context.Context) (M, error)
}

type Repository[M any, H any] interface {
	SaveCurrentMetric(ctx context.Context, metric M, hostId uint) error
	GetLatestMetric(ctx context.Context) (M, error)
	GetLatestMetricByHost(ctx context.Context, hostId uint) (*M, error)
	GetHistoricalMetrics(ctx context.Context, hours float64) ([]H, error)
	GetHistoricalMetricsByHost(ctx context.Context, hostId uint, hours float64) ([]H, error)
}

type Service[M any, H any] struct {
	Logger    *log.Logger
	Name      string
	Collector Collector[M]
	Repo      Repository[M, H]
}

func (s *Service[M, H]) Collect(ctx context.Context) (M, error) {
	s.Logger.Debug("Collecting metrics", "module", s.Name)
	metric, err := s.Collector.Collect(ctx)
	if err != nil {
		s.Logger.Error("Failed to collect metrics", "module", s.Name, "error", err)
		var zero M
		return zero, err
	}
	return metric, nil
}

func (s *Service[M, H]) Save(ctx context.Context, metric M, hostId uint) error {
	s.Logger.Debug("Saving metrics", "module", s.Name, "host_id", hostId)
	err := s.Repo.SaveCurrentMetric(ctx, metric, hostId)
	if err != nil {
		s.Logger.Error("Failed to save metrics", "module", s.Name, "error", err, "host_id", hostId)
		return err
	}
	s.Logger.Debug("Metrics saved", "module", s.Name, "host_id", hostId)
	return nil
}

func (s *Service[M, H]) GetLatest(ctx context.Context) (M, error) {
	metric, err := s.Repo.GetLatestMetric(ctx)
	if err != nil {
		s.Logger.Error("Failed to get latest metrics", "module", s.Name, "error", err)
		var zero M
		return zero, err
	}
	return metric, nil
}

func (s *Service[M, H]) GetLatestByHost(ctx context.Context, hostId uint) (*M, error) {
	return s.Repo.GetLatestMetricByHost(ctx, hostId)
}

func (s *Service[M, H]) GetHistorical(ctx context.Context, hours float64) ([]H, error) {
	metrics, err := s.Repo.GetHistoricalMetrics(ctx, hours)
	if err != nil {
		s.Logger.Error("Failed to get historical metrics", "module", s.Name, "error", err, "hours", hours)
		return nil, err
	}
	return metrics, nil
}

func (s *Service[M, H]) GetHistoricalByHost(ctx context.Context, hostId uint, hours float64) ([]H, error) {
	metrics, err := s.Repo.GetHistoricalMetricsByHost(ctx, hostId, hours)
	if err != nil {
		s.Logger.Error("Failed to get historical metrics by host", "module", s.Name, "error", err, "host_id", hostId, "hours", hours)
		return nil, err
	}
	return metrics, nil
}

func (s *Service[M, H]) CollectAndSave(ctx context.Context, hostId uint) error {
	metric, err := s.Collect(ctx)
	if err != nil {
		return err
	}
	return s.Save(ctx, metric, hostId)
}
