package application

import (
	"context"
	"sync"
	"time"

	"system-stats/internal/modules/history_metrics/core"

	"github.com/charmbracelet/log"
)

// MetricsSaver defines the interface for module services
type MetricsSaver interface {
	CollectAndSave(ctx context.Context) error
}

type metricsCollector struct {
	services []MetricsSaver
}

func NewMetricsCollector(services ...MetricsSaver) *metricsCollector {
	return &metricsCollector{
		services: services,
	}
}

type historicalMetricsService struct {
	logger           *log.Logger
	metricsCollector *metricsCollector
	ticker           *time.Ticker
	stopChan         chan struct{}
	isRunning        bool
	stopMutex        sync.Mutex
}

func NewHistoricalMetricsService(
	logger *log.Logger,
	metricsCollector *metricsCollector,
) core.HistoricalMetricsService {
	return &historicalMetricsService{
		logger:           logger,
		metricsCollector: metricsCollector,
		stopChan:         make(chan struct{}),
	}
}

func (s *historicalMetricsService) CollectAndSaveMetrics(ctx context.Context) error {
	s.logger.Info("Starting metrics collection cycle for all modules")

	for _, service := range s.metricsCollector.services {
		// Collect and save metrics
		err := service.CollectAndSave(ctx)
		if err != nil {
			s.logger.Error("Failed to collect and save metrics", "error", err)
			continue // Continue with other services even if one fails
		}
	}

	s.logger.Info("Metrics collection cycle completed for all modules")
	return nil
}

func (s *historicalMetricsService) StartPeriodicCollection(ctx context.Context, interval time.Duration) error {
	s.stopMutex.Lock()
	defer s.stopMutex.Unlock()

	if s.isRunning {
		return nil
	}

	s.ticker = time.NewTicker(interval)
	s.stopChan = make(chan struct{})
	s.isRunning = true

	if err := s.CollectAndSaveMetrics(ctx); err != nil {
		s.logger.Error("Initial metrics collection failed", "error", err)
	}

	go func() {
		defer s.ticker.Stop()

		for {
			select {
			case <-s.ticker.C:
				if err := s.CollectAndSaveMetrics(ctx); err != nil {
					s.logger.Error("Periodic metrics collection failed", "error", err)
				}
			case <-s.stopChan:
				return
			case <-ctx.Done():
				return
			}
		}
	}()

	s.logger.Info("Started periodic metrics collection", "interval", interval)
	return nil
}

func (s *historicalMetricsService) StopPeriodicCollection() {
	s.stopMutex.Lock()
	defer s.stopMutex.Unlock()

	if !s.isRunning {
		return
	}

	s.isRunning = false

	if s.stopChan != nil {
		close(s.stopChan)
		s.stopChan = nil
	}

	s.logger.Info("Stopped periodic metrics collection")
}
