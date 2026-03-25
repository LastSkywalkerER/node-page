package networkmetrics

import (
	"context"
	"errors"

	"system-stats/internal/app/metrics"
	"system-stats/internal/modules/network/infrastructure/collectors"
	"system-stats/internal/modules/network/infrastructure/entities"
	networkrepos "system-stats/internal/modules/network/infrastructure/repositories"
	"system-stats/internal/modules/network/infrastructure/value_objects"

	"github.com/charmbracelet/log"
)

type Service interface {
	Collect(ctx context.Context) (entities.NetworkMetric, error)
	Save(ctx context.Context, metric entities.NetworkMetric, hostId uint) error
	GetLatest(ctx context.Context) (entities.NetworkMetric, error)
	GetLatestByHost(ctx context.Context, hostId uint) (*entities.NetworkMetric, error)
	GetHistorical(ctx context.Context, hours float64) ([]entities.NetworkMetric, error)
	GetHistoricalByHost(ctx context.Context, hostId uint, hours float64) ([]entities.NetworkMetric, error)
	CollectAndSave(ctx context.Context, hostId uint) error
}

type networkCollectorAdapter struct {
	c *collectors.NetworkMetricsCollector
}

func (a *networkCollectorAdapter) Collect(ctx context.Context) (entities.NetworkMetric, error) {
	return a.c.CollectNetworkMetrics(ctx)
}

type service struct {
	metrics.Service[entities.NetworkMetric, entities.NetworkMetric]
	speedCalculator *value_objects.NetworkSpeedCalculator
}

func NewService(
	logger *log.Logger,
	networkRepository networkrepos.NetworkRepository,
) Service {
	return &service{
		Service: metrics.Service[entities.NetworkMetric, entities.NetworkMetric]{
			Logger:    logger,
			Name:      "network",
			Collector: &networkCollectorAdapter{c: collectors.NewNetworkMetricsCollector(logger)},
			Repo:      networkRepository,
		},
		speedCalculator: value_objects.NewNetworkSpeedCalculator(),
	}
}

func (s *service) Collect(ctx context.Context) (entities.NetworkMetric, error) {
	metric, err := s.Service.Collect(ctx)
	if err != nil {
		return entities.NetworkMetric{}, err
	}

	s.speedCalculator.BeginCalculationBatch()
	for i := range metric.Interfaces {
		iface := &metric.Interfaces[i]
		speed := s.speedCalculator.CalculateSpeed(
			iface.Name,
			iface.BytesSent,
			iface.BytesRecv,
			iface.PacketsSent,
			iface.PacketsRecv,
		)
		iface.SpeedKbpsSent = speed.SpeedKbpsSent
		iface.SpeedKbpsRecv = speed.SpeedKbpsRecv
	}
	s.speedCalculator.EndCalculationBatch()

	return metric, nil
}

func (s *service) GetLatest(ctx context.Context) (entities.NetworkMetric, error) {
	metric, err := s.Service.GetLatest(ctx)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			s.Logger.Debug("Context canceled while getting latest network metrics")
		}
		return entities.NetworkMetric{}, err
	}
	return metric, nil
}

func (s *service) GetLatestByHost(ctx context.Context, hostId uint) (*entities.NetworkMetric, error) {
	metric, err := s.Service.GetLatestByHost(ctx, hostId)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			s.Logger.Debug("Context canceled while getting latest network metrics by host")
		} else {
			s.Logger.Error("Failed to get latest network metrics by host", "error", err, "host_id", hostId)
		}
		return nil, err
	}
	if metric == nil {
		return nil, nil
	}

	s.speedCalculator.BeginCalculationBatch()
	for i := range metric.Interfaces {
		iface := &metric.Interfaces[i]
		speed := s.speedCalculator.CalculateSpeed(
			iface.Name,
			iface.BytesSent,
			iface.BytesRecv,
			iface.PacketsSent,
			iface.PacketsRecv,
		)
		iface.SpeedKbpsSent = speed.SpeedKbpsSent
		iface.SpeedKbpsRecv = speed.SpeedKbpsRecv
	}
	s.speedCalculator.EndCalculationBatch()

	return metric, nil
}

func (s *service) GetHistorical(ctx context.Context, hours float64) ([]entities.NetworkMetric, error) {
	m, err := s.Service.GetHistorical(ctx, hours)
	if err != nil && errors.Is(err, context.Canceled) {
		s.Logger.Debug("Context canceled while getting historical network metrics")
	}
	return m, err
}

func (s *service) GetHistoricalByHost(ctx context.Context, hostId uint, hours float64) ([]entities.NetworkMetric, error) {
	m, err := s.Service.GetHistoricalByHost(ctx, hostId, hours)
	if err != nil && errors.Is(err, context.Canceled) {
		s.Logger.Debug("Context canceled while getting historical network metrics by host")
	}
	return m, err
}

// CollectAndSave overrides the embedded method to use the speed-enriched Collect.
func (s *service) CollectAndSave(ctx context.Context, hostId uint) error {
	metric, err := s.Collect(ctx)
	if err != nil {
		return err
	}
	return s.Save(ctx, metric, hostId)
}
