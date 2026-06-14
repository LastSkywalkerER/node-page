package history

import (
	"context"
	"sync"
	"time"

	"github.com/charmbracelet/log"
	"golang.org/x/sync/errgroup"

	hosts "system-stats/internal/cluster/hosts"
)

// MetricsSaver defines the interface for module services
type MetricsSaver interface {
	CollectAndSave(ctx context.Context, hostId uint) error
}

type metricsCollector struct {
	services []MetricsSaver
}

// NewMetricsCollector creates a new metrics collector from the given savers.
func NewMetricsCollector(services ...MetricsSaver) *metricsCollector {
	return &metricsCollector{
		services: services,
	}
}

type historicalMetricsService struct {
	logger           *log.Logger
	metricsCollector *metricsCollector
	hostService      hosts.Service
	afterCollect     func()
	ticker           *time.Ticker
	stopChan         chan struct{}
	isRunning        bool
	stopMutex        sync.Mutex
}

// NewHistoricalMetricsService creates a new historical metrics service.
func NewHistoricalMetricsService(
	logger *log.Logger,
	metricsCollector *metricsCollector,
	hostService hosts.Service,
) HistoricalMetricsService {
	return &historicalMetricsService{
		logger:           logger,
		metricsCollector: metricsCollector,
		hostService:      hostService,
		stopChan:         make(chan struct{}),
	}
}

// WithAfterCollect sets a hook called after every successful collection cycle.
// Used to publish metrics to the SSE broker without coupling this service to the stream package.
func WithAfterCollect(svc HistoricalMetricsService, fn func()) HistoricalMetricsService {
	s := svc.(*historicalMetricsService)
	s.afterCollect = fn
	return s
}

func (s *historicalMetricsService) CollectAndSaveMetrics(ctx context.Context) error {
	return s.runCycle(ctx, true)
}

// runCycle runs one collection cycle. When persist is true it registers the
// current host and writes every module's metrics to the DB. The live fan-out
// (afterCollect → SSE + cluster stream) runs on EVERY cycle regardless, so live
// updates stay at the collection cadence while the expensive DB writes (and
// their WAL / autovacuum churn) happen only on persist cycles.
func (s *historicalMetricsService) runCycle(ctx context.Context, persist bool) error {
	s.logger.Debug("Starting metrics collection cycle", "persist", persist)

	if persist {
		// Register/update the current host. CollectHostInfo has its own internal
		// timeout for the OS scan; this context only guards the DB upsert.
		regCtx, regCancel := context.WithTimeout(ctx, 20*time.Second)
		host, err := s.hostService.RegisterOrUpdateCurrentHost(regCtx)
		regCancel()
		if err != nil {
			// Skip the DB writes this cycle but still run the live fan-out below.
			s.logger.Error("Failed to register/update current host", "error", err)
		} else {
			hostId := host.ID
			s.logger.Debug("Current host registered/updated", "host_id", hostId, "name", host.Name)

			// Collect + save each module in parallel. Each gets its own 15s
			// deadline so a slow/hung collector (e.g. Docker on macOS) can't stall
			// the goroutine; a per-service failure is logged, never propagated.
			g, gctx := errgroup.WithContext(ctx)
			for _, service := range s.metricsCollector.services {
				svc := service
				g.Go(func() error {
					serviceCtx, cancel := context.WithTimeout(gctx, 15*time.Second)
					defer cancel()
					if err := svc.CollectAndSave(serviceCtx, hostId); err != nil {
						s.logger.Error("Failed to collect and save metrics", "error", err, "host_id", hostId)
					}
					return nil
				})
			}
			_ = g.Wait()
		}
	}

	if s.afterCollect != nil {
		s.afterCollect()
	}
	return nil
}

func (s *historicalMetricsService) StartPeriodicCollection(ctx context.Context, interval, persistInterval time.Duration) error {
	s.stopMutex.Lock()
	defer s.stopMutex.Unlock()

	if s.isRunning {
		return nil
	}
	if interval <= 0 {
		interval = 10 * time.Second
	}
	// Persist every Nth cycle (>=1). persistInterval<=interval => persist every tick.
	persistEvery := 1
	if persistInterval > interval {
		if n := int(persistInterval / interval); n > 1 {
			persistEvery = n
		}
	}

	s.ticker = time.NewTicker(interval)
	s.stopChan = make(chan struct{})
	s.isRunning = true

	// Initial cycle persists so the host row + first metrics land immediately.
	if err := s.runCycle(ctx, true); err != nil {
		s.logger.Error("Initial metrics collection failed", "error", err)
	}

	go func() {
		defer s.ticker.Stop()

		tick := 0
		for {
			select {
			case <-s.ticker.C:
				tick++
				persist := tick%persistEvery == 0
				if err := s.runCycle(ctx, persist); err != nil {
					s.logger.Error("Periodic metrics collection failed", "error", err)
				}
			case <-s.stopChan:
				return
			case <-ctx.Done():
				return
			}
		}
	}()

	s.logger.Info("Started periodic metrics collection", "interval", interval, "persist_every", persistEvery)
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
