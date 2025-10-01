package system

import (
	"context"
	"fmt"
	"time"

	"github.com/charmbracelet/log"

	cpuservice "system-stats/internal/modules/cpu/application"
	diskservice "system-stats/internal/modules/disk/application"
	dockerservice "system-stats/internal/modules/docker/application"
	memoryservice "system-stats/internal/modules/memory/application"
	networkservice "system-stats/internal/modules/network/application"
)

type Service interface {
	CollectAllCurrent(ctx context.Context) (map[string]interface{}, error)
}

type service struct {
	logger         *log.Logger
	cpuService     cpuservice.Service
	memoryService  memoryservice.Service
	diskService    diskservice.Service
	networkService networkservice.Service
	dockerService  dockerservice.Service
}

func NewService(logger *log.Logger, cpuService cpuservice.Service, memoryService memoryservice.Service, diskService diskservice.Service, networkService networkservice.Service, dockerService dockerservice.Service) Service {
	return &service{
		logger:         logger,
		cpuService:     cpuService,
		memoryService:  memoryService,
		diskService:    diskService,
		networkService: networkService,
		dockerService:  dockerService,
	}
}

// CollectAllCurrent collects all current system metrics from individual services.
func (s *service) CollectAllCurrent(ctx context.Context) (map[string]interface{}, error) {
	s.logger.Info("Getting current system metrics")

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
		result := <-results

		switch result.name {
		case "cpu":
			if result.err != nil {
				s.logger.Error("Failed to collect current CPU metrics", "error", result.err)
				return nil, result.err
			}
			cpuMetric = result.metric
			s.logger.Info("Current CPU metrics collected")

		case "memory":
			if result.err != nil {
				s.logger.Error("Failed to collect current memory metrics", "error", result.err)
				return nil, result.err
			}
			memoryMetric = result.metric
			s.logger.Info("Current memory metrics collected")

		case "disk":
			if result.err != nil {
				s.logger.Error("Failed to collect current disk metrics", "error", result.err)
				return nil, result.err
			}
			diskMetric = result.metric
			s.logger.Info("Current disk metrics collected")

		case "network":
			if result.err != nil {
				s.logger.Error("Failed to collect current network metrics", "error", result.err)
				return nil, result.err
			}
			networkMetric = result.metric
			s.logger.Info("Current network metrics collected")

		case "docker":
			if result.err != nil {
				s.logger.Error("Failed to collect current docker metrics", "error", result.err)
				return nil, result.err
			}
			dockerMetric = result.metric
			s.logger.Info("Current docker metrics collected")
		}
	}

	s.logger.Info("Current metrics collected successfully")
	return map[string]interface{}{
		"timestamp": time.Now(),
		"cpu":       cpuMetric,
		"memory":    memoryMetric,
		"disk":      diskMetric,
		"network":   networkMetric,
		"docker":    dockerMetric,
	}, nil
}
