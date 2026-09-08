package system

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/charmbracelet/log"
	"golang.org/x/sync/errgroup"

	cpu "system-stats/internal/metrics/cpu"
	disk "system-stats/internal/metrics/disk"
	docker "system-stats/internal/metrics/docker"
	memory "system-stats/internal/metrics/memory"
	network "system-stats/internal/metrics/network"
)

// Snapshot is ONE collection pass over every local module. The metrics tick
// produces exactly one per cycle and hands the same value to the DB writer,
// the SSE broker, the cluster metric stream and the Prometheus exporter, so
// the OS is scanned once per tick instead of once per consumer.
//
// A nil module means that collector failed this pass; consumers omit it
// rather than shipping a zero value (the SSE merge is per-key and a null
// would clobber a widget's last good state).
type Snapshot struct {
	Timestamp    time.Time
	CPU          *cpu.CPUMetric
	Memory       *memory.MemoryMetric
	Disk         *disk.DiskMetric
	Network      *network.NetworkMetric
	Docker       *docker.DockerMetric
	Applications []docker.DockerApplication
}

// Payload renders the live-metrics JSON object served by GET /metrics/current
// and pushed over SSE. collectingHostID > 0 tags the event with the row id of
// the host that produced it (the SSE client filters on it).
func (s Snapshot) Payload(collectingHostID uint) map[string]interface{} {
	out := map[string]interface{}{"timestamp": s.Timestamp}
	if s.CPU != nil {
		out["cpu"] = *s.CPU
	}
	if s.Memory != nil {
		out["memory"] = *s.Memory
	}
	if s.Disk != nil {
		out["disk"] = *s.Disk
	}
	if s.Network != nil {
		out["network"] = *s.Network
	}
	if s.Docker != nil {
		out["docker"] = *s.Docker
		// The application projection rides the live stream so the frontend can
		// sync its applications caches in lockstep with the containers. It is
		// derived from the same docker metric and never replicated (peers
		// rebuild apps from the replicated docker rows).
		apps := s.Applications
		if apps == nil {
			apps = []docker.DockerApplication{}
		}
		out["applications"] = apps
	}
	if collectingHostID > 0 {
		out["collecting_host_id"] = collectingHostID
	}
	return out
}

type Service interface {
	// CollectAllCurrent returns the live-metrics payload. It serves the most
	// recent tick's snapshot while that is still fresh and only scans the OS
	// itself when no fresh snapshot exists (metrics not started yet, or the
	// caller is polling faster than the tick).
	CollectAllCurrent(ctx context.Context) (map[string]interface{}, error)
	// CollectSnapshot scans every module once (in parallel, best-effort per
	// module) and records the result as the latest snapshot.
	CollectSnapshot(ctx context.Context) (Snapshot, error)
	// SaveSnapshot persists every module present in the snapshot for hostID.
	// Per-module failures are logged, never propagated.
	SaveSnapshot(ctx context.Context, snap Snapshot, hostID uint) error
	// Latest returns the most recent snapshot, if any tick has completed.
	Latest() (Snapshot, bool)
}

// defaultFreshWindow bounds how old a snapshot CollectAllCurrent may serve
// before it scans the OS itself; WithFreshWindow aligns it with the tick.
const defaultFreshWindow = 15 * time.Second

type service struct {
	logger         *log.Logger
	cpuService     cpu.Service
	memoryService  memory.Service
	diskService    disk.Service
	networkService network.Service
	dockerService  docker.Service

	mu          sync.RWMutex
	latest      Snapshot
	haveLatest  bool
	freshWindow time.Duration

	// inflight coalesces concurrent CollectSnapshot calls (the ticker and an
	// on-demand GET /metrics/current landing together). Two overlapping
	// passes would interleave inside the rate calculators — the network
	// speed baseline, gopsutil's CPU-times baseline — and one of them would
	// compute a delta against the other's newer sample (an unsigned wrap:
	// a 10^16 kbps spike). The second caller waits for the first pass and
	// returns its snapshot instead.
	flightMu sync.Mutex
	inflight chan struct{}
}

// NewService creates a new system service.
func NewService(
	logger *log.Logger,
	cpuSvc cpu.Service,
	memSvc memory.Service,
	diskSvc disk.Service,
	netSvc network.Service,
	dockerSvc docker.Service,
) Service {
	return &service{
		logger:         logger,
		cpuService:     cpuSvc,
		memoryService:  memSvc,
		diskService:    diskSvc,
		networkService: netSvc,
		dockerService:  dockerSvc,
		freshWindow:    defaultFreshWindow,
	}
}

// WithFreshWindow sets how old the latest snapshot may be for
// CollectAllCurrent to serve it instead of scanning. Pass the metrics tick
// interval plus a margin so an on-demand read never races the ticker.
func WithFreshWindow(svc Service, d time.Duration) Service {
	if s, ok := svc.(*service); ok && d > 0 {
		s.mu.Lock()
		s.freshWindow = d
		s.mu.Unlock()
	}
	return svc
}

func (s *service) Latest() (Snapshot, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.latest, s.haveLatest
}

// CollectAllCurrent collects all current system metrics from individual services.
func (s *service) CollectAllCurrent(ctx context.Context) (map[string]interface{}, error) {
	s.mu.RLock()
	snap, ok, window := s.latest, s.haveLatest, s.freshWindow
	s.mu.RUnlock()
	if ok && time.Since(snap.Timestamp) < window {
		s.logger.Debug("Serving latest metrics snapshot", "age", time.Since(snap.Timestamp))
		return snap.Payload(0), nil
	}
	snap, err := s.CollectSnapshot(ctx)
	if err != nil {
		return nil, err
	}
	return snap.Payload(0), nil
}

func (s *service) CollectSnapshot(ctx context.Context) (Snapshot, error) {
	s.flightMu.Lock()
	if done := s.inflight; done != nil {
		s.flightMu.Unlock()
		select {
		case <-done:
			if snap, ok := s.Latest(); ok {
				return snap, nil
			}
			return Snapshot{}, errors.New("metrics collection in flight failed")
		case <-ctx.Done():
			return Snapshot{}, ctx.Err()
		}
	}
	done := make(chan struct{})
	s.inflight = done
	s.flightMu.Unlock()
	defer func() {
		s.flightMu.Lock()
		s.inflight = nil
		s.flightMu.Unlock()
		close(done)
	}()
	return s.collectSnapshot(ctx)
}

func (s *service) collectSnapshot(ctx context.Context) (Snapshot, error) {
	s.logger.Debug("Collecting system metrics snapshot")

	type collectResult struct {
		name   string
		metric interface{}
		err    error
	}
	results := make(chan collectResult, 5)

	// Each module runs in its own goroutine; a panic or error in one module
	// is reported for that module only. One slow/broken module (classic: a
	// wedged docker daemon) must not suppress the whole SSE / replication
	// batch - the other modules still ship.
	collectMetric := func(name string, collectFunc func() (interface{}, error)) {
		defer func() {
			if r := recover(); r != nil {
				results <- collectResult{name: name, err: fmt.Errorf("panic in %s collection: %v", name, r)}
			}
		}()
		metric, err := collectFunc()
		results <- collectResult{name: name, metric: metric, err: err}
	}
	go collectMetric("cpu", func() (interface{}, error) { return s.cpuService.Collect(ctx) })
	go collectMetric("memory", func() (interface{}, error) { return s.memoryService.Collect(ctx) })
	go collectMetric("disk", func() (interface{}, error) { return s.diskService.Collect(ctx) })
	go collectMetric("network", func() (interface{}, error) { return s.networkService.Collect(ctx) })
	go collectMetric("docker", func() (interface{}, error) { return s.dockerService.Collect(ctx) })

	snap := Snapshot{Timestamp: time.Now()}
	for i := 0; i < 5; i++ {
		var result collectResult
		select {
		case result = <-results:
		case <-ctx.Done():
			return Snapshot{}, ctx.Err()
		}
		if result.err != nil {
			s.logger.Error("Failed to collect current metrics", "module", result.name, "error", result.err)
			continue
		}
		switch m := result.metric.(type) {
		case cpu.CPUMetric:
			snap.CPU = &m
		case memory.MemoryMetric:
			snap.Memory = &m
		case disk.DiskMetric:
			snap.Disk = &m
		case network.NetworkMetric:
			snap.Network = &m
		case docker.DockerMetric:
			snap.Docker = &m
		}
	}
	if snap.Docker != nil {
		snap.Applications = docker.BuildApplications(snap.Docker)
	}

	s.mu.Lock()
	s.latest = snap
	s.haveLatest = true
	s.mu.Unlock()
	s.logger.Debug("System metrics snapshot collected")
	return snap, nil
}

// SaveSnapshot writes every module present in the snapshot in parallel. Each
// gets its own deadline so a slow DB write for one module can't stall the
// others; failures are logged per module and never propagated.
func (s *service) SaveSnapshot(ctx context.Context, snap Snapshot, hostID uint) error {
	g, gctx := errgroup.WithContext(ctx)
	save := func(name string, fn func(context.Context) error) {
		g.Go(func() error {
			saveCtx, cancel := context.WithTimeout(gctx, 15*time.Second)
			defer cancel()
			if err := fn(saveCtx); err != nil {
				s.logger.Error("Failed to save metrics", "module", name, "error", err, "host_id", hostID)
			}
			return nil
		})
	}
	if snap.CPU != nil {
		save("cpu", func(c context.Context) error { return s.cpuService.Save(c, *snap.CPU, hostID) })
	}
	if snap.Memory != nil {
		save("memory", func(c context.Context) error { return s.memoryService.Save(c, *snap.Memory, hostID) })
	}
	if snap.Disk != nil {
		save("disk", func(c context.Context) error { return s.diskService.Save(c, *snap.Disk, hostID) })
	}
	if snap.Network != nil {
		save("network", func(c context.Context) error { return s.networkService.Save(c, *snap.Network, hostID) })
	}
	if snap.Docker != nil {
		save("docker", func(c context.Context) error { return s.dockerService.Save(c, *snap.Docker, hostID) })
	}
	return g.Wait()
}
