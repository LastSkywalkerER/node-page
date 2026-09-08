package history

import (
	"context"
	"sync"
	"time"

	"github.com/charmbracelet/log"

	hosts "system-stats/internal/cluster/hosts"
	system "system-stats/internal/platform/system"
)

// SnapshotSource scans every module once and persists a snapshot — the
// system service. The tick owns the cadence; the source owns the modules.
type SnapshotSource interface {
	CollectSnapshot(ctx context.Context) (system.Snapshot, error)
	SaveSnapshot(ctx context.Context, snap system.Snapshot, hostID uint) error
}

// AfterCollectFunc receives every tick's snapshot plus the local host row as
// of the most recent successful registration (nil until one succeeded).
type AfterCollectFunc func(snap system.Snapshot, host *hosts.Host)

type historicalMetricsService struct {
	logger       *log.Logger
	source       SnapshotSource
	hostService  hosts.Service
	afterCollect AfterCollectFunc
	ticker       *time.Ticker
	stopChan     chan struct{}
	isRunning    bool
	stopMutex    sync.Mutex

	hostMu   sync.Mutex
	lastHost *hosts.Host
}

// NewHistoricalMetricsService creates a new historical metrics service.
func NewHistoricalMetricsService(
	logger *log.Logger,
	source SnapshotSource,
	hostService hosts.Service,
) HistoricalMetricsService {
	return &historicalMetricsService{
		logger:      logger,
		source:      source,
		hostService: hostService,
		stopChan:    make(chan struct{}),
	}
}

// WithAfterCollect sets a hook called after every collection cycle with that
// cycle's snapshot. Used to publish metrics to the SSE broker and the cluster
// metric stream without coupling this service to those packages.
func WithAfterCollect(svc HistoricalMetricsService, fn AfterCollectFunc) HistoricalMetricsService {
	s := svc.(*historicalMetricsService)
	s.afterCollect = fn
	return s
}

func (s *historicalMetricsService) CollectAndSaveMetrics(ctx context.Context) error {
	return s.runCycle(ctx, true)
}

// runCycle runs one collection cycle: ONE scan of every module, then — on
// persist cycles only — the host registration and the DB writes, then the
// live fan-out (afterCollect → SSE + cluster stream) on EVERY cycle. Live
// updates stay at the collection cadence while the expensive DB writes (and
// their WAL / autovacuum churn) happen only on persist cycles, and the OS is
// never scanned twice for the same tick.
func (s *historicalMetricsService) runCycle(ctx context.Context, persist bool) error {
	s.logger.Debug("Starting metrics collection cycle", "persist", persist)

	collectCtx, collectCancel := context.WithTimeout(ctx, 15*time.Second)
	snap, err := s.source.CollectSnapshot(collectCtx)
	collectCancel()
	if err != nil {
		s.logger.Error("Metrics collection failed", "error", err)
		return err
	}

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
			s.setLastHost(host)
			s.logger.Debug("Current host registered/updated", "host_id", host.ID, "name", host.Name)
			saveCtx, saveCancel := context.WithTimeout(ctx, 20*time.Second)
			_ = s.source.SaveSnapshot(saveCtx, snap, host.ID)
			saveCancel()
		}
	}

	if s.afterCollect != nil {
		s.afterCollect(snap, s.getLastHost())
	}
	return nil
}

func (s *historicalMetricsService) setLastHost(h *hosts.Host) {
	if h == nil {
		return
	}
	cp := *h
	s.hostMu.Lock()
	s.lastHost = &cp
	s.hostMu.Unlock()
}

func (s *historicalMetricsService) getLastHost() *hosts.Host {
	s.hostMu.Lock()
	defer s.hostMu.Unlock()
	return s.lastHost
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
