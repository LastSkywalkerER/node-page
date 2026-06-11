package network

import (
	"context"
	"errors"

	appmetrics "system-stats/internal/app/metrics"

	"github.com/charmbracelet/log"
)

type Service interface {
	Collect(ctx context.Context) (NetworkMetric, error)
	Save(ctx context.Context, metric NetworkMetric, hostId uint) error
	GetLatest(ctx context.Context) (NetworkMetric, error)
	GetLatestByHost(ctx context.Context, hostId uint) (*NetworkMetric, error)
	GetHistorical(ctx context.Context, hours float64) ([]NetworkMetric, error)
	GetHistoricalByHost(ctx context.Context, hostId uint, hours float64) ([]NetworkMetric, error)
	CollectAndSave(ctx context.Context, hostId uint) error
}

type networkCollectorAdapter struct {
	c *networkCollector
}

func (a *networkCollectorAdapter) Collect(ctx context.Context) (NetworkMetric, error) {
	return a.c.Collect(ctx)
}

type service struct {
	appmetrics.Service[NetworkMetric, NetworkMetric]
	speedCalculator *NetworkSpeedCalculator
}

func NewService(logger *log.Logger, repo Repository) Service {
	return &service{
		Service: appmetrics.Service[NetworkMetric, NetworkMetric]{
			Logger:    logger,
			Name:      "network",
			Collector: &networkCollectorAdapter{c: newNetworkCollector(logger)},
			Repo:      repo,
		},
		speedCalculator: NewNetworkSpeedCalculator(),
	}
}

func (s *service) Collect(ctx context.Context) (NetworkMetric, error) {
	metric, err := s.Service.Collect(ctx)
	if err != nil {
		return NetworkMetric{}, err
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

func (s *service) GetLatest(ctx context.Context) (NetworkMetric, error) {
	metric, err := s.Service.GetLatest(ctx)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			s.Logger.Debug("Context canceled while getting latest network metrics")
		}
		return NetworkMetric{}, err
	}
	return metric, nil
}

func (s *service) GetLatestByHost(ctx context.Context, hostId uint) (*NetworkMetric, error) {
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
		// Rows written with rates already computed (the agent's collector, or
		// connector-fed hosts whose counters live hypervisor-side) keep them:
		// the read-side calculator keys its state by interface NAME only, so
		// re-deriving here would mix samples of different hosts' "net0"/"eth0".
		if iface.SpeedKbpsSent != 0 || iface.SpeedKbpsRecv != 0 {
			continue
		}
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

func (s *service) GetHistorical(ctx context.Context, hours float64) ([]NetworkMetric, error) {
	m, err := s.Service.GetHistorical(ctx, hours)
	if err != nil && errors.Is(err, context.Canceled) {
		s.Logger.Debug("Context canceled while getting historical network metrics")
	}
	return m, err
}

func (s *service) GetHistoricalByHost(ctx context.Context, hostId uint, hours float64) ([]NetworkMetric, error) {
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
