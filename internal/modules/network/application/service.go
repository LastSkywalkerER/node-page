package networkmetrics

import (
	"context"
	"errors"

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

type service struct {
	logger            *log.Logger
	collector         *collectors.NetworkMetricsCollector
	networkRepository networkrepos.NetworkRepository
	speedCalculator   *value_objects.NetworkSpeedCalculator
}

func NewService(
	logger *log.Logger,
	networkRepository networkrepos.NetworkRepository,
) Service {
	return &service{
		logger:            logger,
		collector:         collectors.NewNetworkMetricsCollector(logger),
		networkRepository: networkRepository,
		speedCalculator:   value_objects.NewNetworkSpeedCalculator(),
	}
}

func (s *service) Collect(ctx context.Context) (entities.NetworkMetric, error) {
	s.logger.Debug("Collecting network metrics")
	metric, err := s.collector.CollectNetworkMetrics(ctx)
	if err != nil {
		s.logger.Error("Failed to collect network metrics", "error", err)
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

	s.logger.Debug("Network metrics collected", "interfaces_count", len(metric.Interfaces))
	return metric, nil
}

func (s *service) Save(ctx context.Context, metric entities.NetworkMetric, hostId uint) error {
	s.logger.Debug("Saving network metrics", "interfaces_count", len(metric.Interfaces))
	err := s.networkRepository.SaveCurrentMetric(ctx, metric, hostId)
	if err != nil {
		s.logger.Error("Failed to save network metrics", "error", err)
		return err
	}
	s.logger.Debug("Network metrics saved")
	return nil
}

func (s *service) GetLatest(ctx context.Context) (entities.NetworkMetric, error) {
	metric, err := s.networkRepository.GetLatestMetric(ctx)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			s.logger.Debug("Context canceled while getting latest network metrics")
		} else {
			s.logger.Error("Failed to get latest network metrics", "error", err)
		}
		return entities.NetworkMetric{}, err
	}
	return metric, nil
}

func (s *service) GetLatestByHost(ctx context.Context, hostId uint) (*entities.NetworkMetric, error) {
	metric, err := s.networkRepository.GetLatestMetricByHost(ctx, hostId)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			s.logger.Debug("Context canceled while getting latest network metrics by host")
		} else {
			s.logger.Error("Failed to get latest network metrics by host", "error", err, "host_id", hostId)
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
	metrics, err := s.networkRepository.GetHistoricalMetrics(ctx, hours)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			s.logger.Debug("Context canceled while getting historical network metrics")
		} else {
			s.logger.Error("Failed to get historical network metrics", "error", err, "hours", hours)
		}
		return nil, err
	}
	return metrics, nil
}

func (s *service) GetHistoricalByHost(ctx context.Context, hostId uint, hours float64) ([]entities.NetworkMetric, error) {
	metrics, err := s.networkRepository.GetHistoricalMetricsByHost(ctx, hostId, hours)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			s.logger.Debug("Context canceled while getting historical network metrics by host")
		} else {
			s.logger.Error("Failed to get historical network metrics by host", "error", err, "host_id", hostId, "hours", hours)
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
