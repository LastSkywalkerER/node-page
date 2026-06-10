package system

import (
	"context"
	"fmt"
	"time"

	"github.com/charmbracelet/log"

	cpu "system-stats/internal/metrics/cpu"
	disk "system-stats/internal/metrics/disk"
	docker "system-stats/internal/metrics/docker"
	memory "system-stats/internal/metrics/memory"
	network "system-stats/internal/metrics/network"
)

type Service interface {
	CollectAllCurrent(ctx context.Context) (map[string]interface{}, error)
}

type service struct {
	logger         *log.Logger
	cpuService     cpu.Service
	memoryService  memory.Service
	diskService    disk.Service
	networkService network.Service
	dockerService  docker.Service
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
	}
}

// CollectAllCurrent collects all current system metrics from individual services.
func (s *service) CollectAllCurrent(ctx context.Context) (map[string]interface{}, error) {
	s.logger.Debug("Getting current system metrics")

	// Structure for parallel collection results
	type collectResult struct {
		name   string
		metric interface{}
		err    error
	}

	results := make(chan collectResult, 5)

	// Function for safe metrics collection
	collectMetric := func(name string, collectFunc func() (interface{}, error)) {
		defer func() {
			if r := recover(); r != nil {
				results <- collectResult{name: name, err: fmt.Errorf("panic in %s collection: %v", name, r)}
			}
		}()
		metric, err := collectFunc()
		results <- collectResult{name: name, metric: metric, err: err}
	}

	// Start parallel collection of all metrics
	go collectMetric("cpu", func() (interface{}, error) {
		return s.cpuService.Collect(ctx)
	})

	go collectMetric("memory", func() (interface{}, error) {
		return s.memoryService.Collect(ctx)
	})

	go collectMetric("disk", func() (interface{}, error) {
		return s.diskService.Collect(ctx)
	})

	go collectMetric("network", func() (interface{}, error) {
		return s.networkService.Collect(ctx)
	})

	go collectMetric("docker", func() (interface{}, error) {
		return s.dockerService.Collect(ctx)
	})

	// Collect results
	var cpuMetric interface{}
	var memoryMetric interface{}
	var diskMetric interface{}
	var networkMetric interface{}
	var dockerMetric interface{}

	for i := 0; i < 5; i++ {
		var result collectResult
		select {
		case result = <-results:
		case <-ctx.Done():
			return nil, ctx.Err()
		}

		switch result.name {
		case "cpu":
			if result.err != nil {
				s.logger.Error("Failed to collect current CPU metrics", "error", result.err)
				return nil, result.err
			}
			cpuMetric = result.metric
			s.logger.Debug("Current CPU metrics collected")

		case "memory":
			if result.err != nil {
				s.logger.Error("Failed to collect current memory metrics", "error", result.err)
				return nil, result.err
			}
			memoryMetric = result.metric
			s.logger.Debug("Current memory metrics collected")

		case "disk":
			if result.err != nil {
				s.logger.Error("Failed to collect current disk metrics", "error", result.err)
				return nil, result.err
			}
			diskMetric = result.metric
			s.logger.Debug("Current disk metrics collected")

		case "network":
			if result.err != nil {
				s.logger.Error("Failed to collect current network metrics", "error", result.err)
				return nil, result.err
			}
			networkMetric = result.metric
			s.logger.Debug("Current network metrics collected")

		case "docker":
			if result.err != nil {
				s.logger.Error("Failed to collect current docker metrics", "error", result.err)
				return nil, result.err
			}
			dockerMetric = result.metric
			s.logger.Debug("Current docker metrics collected")
		}
	}

	// Pre-build the application projection so the live SSE stream carries it
	// (the frontend syncs it into the applications caches for lockstep 5s updates,
	// instead of the slower independent REST poll). Derived from the same docker
	// metric; not replicated (peers rebuild apps from the replicated docker rows).
	var applications []docker.DockerApplication
	if dm, ok := dockerMetric.(docker.DockerMetric); ok {
		applications = docker.BuildApplications(&dm)
	}

	s.logger.Debug("Current metrics collected successfully")
	return map[string]interface{}{
		"timestamp":    time.Now(),
		"cpu":          cpuMetric,
		"memory":       memoryMetric,
		"disk":         diskMetric,
		"network":      networkMetric,
		"docker":       dockerMetric,
		"applications": applications,
	}, nil
}
