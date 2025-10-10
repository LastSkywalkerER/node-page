package networkmetrics

import (
	"context"

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
	GetHistorical(ctx context.Context, hours float64) ([]interface{}, error)
	GetHistoricalByHost(ctx context.Context, hostId uint, hours float64) ([]interface{}, error)
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
	s.logger.Info("Collecting network metrics")
	metric, err := s.collector.CollectNetworkMetrics(ctx)
	if err != nil {
		s.logger.Error("Failed to collect network metrics", "error", err)
		return entities.NetworkMetric{}, err
	}

	// Begin batch calculation with consistent timestamp
	s.speedCalculator.BeginCalculationBatch()

	// Calculate speeds for each interface
	for i := range metric.Interfaces {
		iface := &metric.Interfaces[i]
		speed := s.speedCalculator.CalculateSpeed(
			iface.Name,
			iface.BytesSent,
			iface.BytesRecv,
			iface.PacketsSent,
			iface.PacketsRecv,
		)

		// Store calculated speeds in the interface
		iface.SpeedKbpsSent = speed.SpeedKbpsSent
		iface.SpeedKbpsRecv = speed.SpeedKbpsRecv

		s.logger.Info("Calculated speeds for interface",
			"interface", iface.Name,
			"speed_kbps_sent", speed.SpeedKbpsSent,
			"speed_kbps_recv", speed.SpeedKbpsRecv)
	}

	// End batch calculation and update timestamp
	s.speedCalculator.EndCalculationBatch()

	s.logger.Info("Network metrics collected successfully", "interfaces_count", len(metric.Interfaces))
	return metric, nil
}

func (s *service) Save(ctx context.Context, metric entities.NetworkMetric, hostId uint) error {
	s.logger.Info("Saving network metrics to repository", "interfaces_count", len(metric.Interfaces))

	err := s.networkRepository.SaveCurrentMetric(ctx, metric, hostId)
	if err != nil {
		s.logger.Error("Failed to save network metrics", "error", err)
		return err
	}

	s.logger.Info("All network metrics saved successfully")
	return nil
}

func (s *service) GetLatest(ctx context.Context) (entities.NetworkMetric, error) {
	s.logger.Info("Getting latest network metrics")
	metric, err := s.networkRepository.GetLatestMetric(ctx)
	if err != nil {
		s.logger.Error("Failed to get latest network metrics", "error", err)
		return entities.NetworkMetric{}, err
	}
	s.logger.Info("Latest network metrics retrieved successfully")
	return metric, nil
}

func (s *service) GetHistorical(ctx context.Context, hours float64) ([]interface{}, error) {
	s.logger.Info("Getting historical network metrics", "hours", hours)
	metrics, err := s.networkRepository.GetHistoricalMetrics(ctx, hours)
	if err != nil {
		s.logger.Error("Failed to get historical network metrics", "error", err, "hours", hours)
		return nil, err
	}
	s.logger.Info("Historical network metrics retrieved successfully", "count", len(metrics))

	// Debug: print first metric structure
	if len(metrics) > 0 {
		println("DEBUG SERVICE: First historical metric has", len(metrics[0].Interfaces), "interfaces")
	}

	// Convert to []interface{} for compatibility
	result := make([]interface{}, len(metrics))
	for i, metric := range metrics {
		result[i] = metric
	}
	return result, nil
}

func (s *service) GetHistoricalByHost(ctx context.Context, hostId uint, hours float64) ([]interface{}, error) {
	s.logger.Info("Getting historical network metrics by host", "host_id", hostId, "hours", hours)
	metrics, err := s.networkRepository.GetHistoricalMetricsByHost(ctx, hostId, hours)
	if err != nil {
		s.logger.Error("Failed to get historical network metrics by host", "error", err, "host_id", hostId, "hours", hours)
		return nil, err
	}
	s.logger.Info("Historical network metrics by host retrieved successfully", "host_id", hostId, "count", len(metrics))

	// Convert to []interface{} for compatibility
	result := make([]interface{}, len(metrics))
	for i, metric := range metrics {
		result[i] = metric
	}
	return result, nil
}

func (s *service) CollectAndSave(ctx context.Context, hostId uint) error {
	metric, err := s.Collect(ctx)
	if err != nil {
		return err
	}
	return s.Save(ctx, metric, hostId)
}
