/**
 * Package collectors provides metric collection implementations for various data sources.
 * This package contains collectors for system metrics, Docker containers, and other
 * external data sources used by the monitoring application.
 */
package collectors

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/charmbracelet/log"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/client"

	"system-stats/internal/modules/docker/domain/repositories"
	"system-stats/internal/modules/docker/infrastructure/entities"
)

/**
 * dockerMetricsCollector implements the DockerMetricsCollector interface.
 * This collector gathers Docker container statistics and status information
 * using the Docker API client with caching for availability checks and CPU calculations.
 */
type dockerMetricsCollector struct {
	/** logger provides structured logging for Docker operations */
	logger *log.Logger

	/** dockerAvailable caches whether Docker daemon is accessible */
	dockerAvailable bool

	/** lastCheck tracks when Docker availability was last verified */
	lastCheck time.Time

	/** checkInterval defines how often to recheck Docker availability */
	checkInterval time.Duration

	/** containerCPUCache stores previous CPU stats for percentage calculations */
	containerCPUCache map[string]cpuStatsCache

	/** cacheMutex protects concurrent access to CPU cache */
	cacheMutex sync.RWMutex
}

/**
 * cpuStatsCache stores CPU statistics for percentage calculation.
 * This structure holds previous CPU usage data needed to compute CPU percentages.
 */
type cpuStatsCache struct {
	/** totalUsage stores the total CPU usage from previous measurement */
	totalUsage uint64

	/** systemUsage stores the system CPU usage from previous measurement */
	systemUsage uint64

	/** timestamp indicates when the previous measurement was taken */
	timestamp time.Time
}

/**
 * NewDockerMetricsCollector creates a new Docker metrics collector instance.
 * This constructor initializes the collector with default cache settings.
 *
 * @param logger The logger instance for logging collection operations
 * @return repositories.DockerMetricsCollector Returns the initialized Docker collector
 */
func NewDockerMetricsCollector(logger *log.Logger) repositories.DockerMetricsCollector {
	return &dockerMetricsCollector{
		logger:            logger,
		checkInterval:     5 * time.Second, // Check Docker availability every 5 seconds
		containerCPUCache: make(map[string]cpuStatsCache),
		// cacheMutex is initialized automatically (zero value)
	}
}

/**
 * IsDockerAvailable checks if the Docker daemon is accessible and running.
 * This method caches the result for 5 seconds to avoid excessive API calls.
 *
 * @param ctx The context for the operation
 * @return bool Returns true if Docker is available, false otherwise
 */
func (c *dockerMetricsCollector) IsDockerAvailable(ctx context.Context) bool {
	c.cacheMutex.Lock()
	defer c.cacheMutex.Unlock()

	now := time.Now()

	// If less time has passed since the last check, return cached result
	if now.Sub(c.lastCheck) < c.checkInterval && c.lastCheck.After(time.Time{}) {
		return c.dockerAvailable
	}

	c.lastCheck = now

	/** cli is the Docker API client used to communicate with the Docker daemon */
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		c.dockerAvailable = false
		return false
	}
	defer cli.Close()

	// Check Docker availability
	_, err = cli.Ping(ctx)
	c.dockerAvailable = err == nil

	return c.dockerAvailable
}

/**
 * CollectDockerMetrics gathers Docker container statistics and status information.
 * This method retrieves information about all running containers including their
 * resource usage, network statistics, and metadata from the Docker API.
 *
 * @param ctx The context for the operation, used for cancellation and timeouts
 * @return entities.DockerMetric The collected Docker metrics data
 * @return error Returns an error if Docker API calls fail
 */
func (c *dockerMetricsCollector) CollectDockerMetrics(ctx context.Context) (entities.DockerMetric, error) {
	c.logger.Info("Collecting Docker container metrics")

	if !c.IsDockerAvailable(ctx) {
		c.logger.Warn("Docker is not available, returning empty metrics")
		return entities.DockerMetric{
			DockerAvailable: false,
			Error:           "Docker is not available or not running",
		}, nil
	}

	// Create Docker client
	/** cli is the Docker API client used to communicate with the Docker daemon */
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return entities.DockerMetric{
			DockerAvailable: false,
			Error:           fmt.Sprintf("Failed to create Docker client: %v", err),
		}, nil
	}
	defer cli.Close()

	// Get list of all containers
	containers, err := cli.ContainerList(ctx, types.ContainerListOptions{All: true})
	if err != nil {
		return entities.DockerMetric{
			DockerAvailable: true,
			Error:           fmt.Sprintf("Failed to list containers: %v", err),
		}, nil
	}

	// Structure for parallel container processing results
	type containerResult struct {
		container entities.DockerContainer
		isRunning bool
		err       error
	}

	results := make(chan containerResult, len(containers))
	var runningCount int32 = 0

	// Function for parallel processing of one container
	processContainer := func(containerInfo types.Container) {
		defer func() {
			if r := recover(); r != nil {
				results <- containerResult{err: fmt.Errorf("panic processing container %s: %v", containerInfo.ID, r)}
			}
		}()

		// Get detailed container information
		containerJSON, err := cli.ContainerInspect(ctx, containerInfo.ID)
		if err != nil {
			results <- containerResult{err: fmt.Errorf("failed to inspect container %s: %v", containerInfo.ID, err)}
			return
		}

		// Output containerJSON to console
		containerJSONBytes, _ := json.Marshal(containerJSON)
		c.logger.Debug("Container JSON details", "container_id", containerInfo.ID, "json", string(containerJSONBytes))

		// Parse container name (remove "/" prefix)
		var name string
		if len(containerInfo.Names) > 0 {
			name = strings.TrimPrefix(containerInfo.Names[0], "/")
		} else {
			name = "unknown"
		}

		// Convert ports (with nil protection)
		var ports []entities.DockerPort
		if containerInfo.Ports != nil {
			ports = c.convertPorts(containerInfo.Ports)
		} else {
			ports = make([]entities.DockerPort, 0)
		}

		// Get CPU limit from container configuration
		cpuLimit := c.getCPULimit(containerJSON)

		// Get real statistics for running containers
		var containerStats entities.DockerStats
		isRunning := containerInfo.State == "running"

		if isRunning {
			// Safely get statistics with panic protection
			containerStats = c.getContainerResourceStats(ctx, cli, containerInfo.ID, cpuLimit)
		} else {
			// For stopped containers, use empty statistics but keep CPU limit
			containerStats = entities.DockerStats{
				CPULimit: cpuLimit,
			}
		}

		// Safely get container ID
		containerID := containerInfo.ID
		if len(containerID) > 12 {
			containerID = containerID[:12]
		}

		finishedAt := c.parseContainerFinishedTime(containerJSON.State.FinishedAt)
		c.logger.Debug("Container finished time", "container_id", containerID, "finished_at_raw", containerJSON.State.FinishedAt, "finished_at_parsed", finishedAt)

		dockerContainer := entities.DockerContainer{
			ID:         containerID,
			Name:       name,
			Image:      containerInfo.Image,
			State:      containerInfo.State,
			Status:     containerInfo.Status,
			Ports:      ports,
			Stats:      containerStats,
			Created:    c.parseContainerCreatedTime(containerJSON.Created),
			FinishedAt: finishedAt,
		}

		results <- containerResult{
			container: dockerContainer,
			isRunning: isRunning,
		}
	}

	// Start parallel processing of all containers
	for _, containerInfo := range containers {
		go processContainer(containerInfo)
	}

	// Collect results
	dockerContainers := make([]entities.DockerContainer, 0, len(containers))
	for i := 0; i < len(containers); i++ {
		result := <-results
		if result.err != nil {
			c.logger.Warn("Failed to process container", "error", result.err)
			continue
		}
		if result.isRunning {
			atomic.AddInt32(&runningCount, 1)
		}
		dockerContainers = append(dockerContainers, result.container)
	}

	c.logger.Info("Docker metrics collected successfully", "total_containers", len(containers), "running_containers", int(runningCount))

	// Log full content of all containers
	for i, container := range dockerContainers {
		c.logger.Debug("Docker container details",
			"index", i,
			"id", container.ID,
			"name", container.Name,
			"image", container.Image,
			"state", container.State,
			"status", container.Status,
			"ports_count", len(container.Ports),
			"cpu_percent", container.Stats.CPUPercent,
			"cpu_limit", container.Stats.CPULimit,
			"cpu_percent_of_limit", container.Stats.CPUPercentOfLimit,
			"memory_usage", container.Stats.MemoryUsage,
			"memory_limit", container.Stats.MemoryLimit,
			"memory_percent", container.Stats.MemoryPercent,
			"network_rx", container.Stats.NetworkRx,
			"network_tx", container.Stats.NetworkTx,
			"block_read", container.Stats.BlockRead,
			"block_write", container.Stats.BlockWrite,
			"created", container.Created)

		// Log ports separately if they exist
		for j, port := range container.Ports {
			c.logger.Debug("Docker container port",
				"container_name", container.Name,
				"port_index", j,
				"private_port", port.PrivatePort,
				"public_port", port.PublicPort,
				"type", port.Type,
				"ip", port.IP)
		}
	}

	return entities.DockerMetric{
		Containers:        dockerContainers,
		TotalContainers:   len(containers),
		RunningContainers: int(runningCount),
		DockerAvailable:   true,
	}, nil
}

// getCPULimit extracts CPU limit from container configuration
func (c *dockerMetricsCollector) getCPULimit(containerJSON types.ContainerJSON) float64 {
	hostConfig := containerJSON.HostConfig

	// Check NanoCPUs (for --cpus flag)
	if hostConfig.NanoCPUs > 0 {
		return float64(hostConfig.NanoCPUs) / 1000000000.0 // Convert from nanocpus to CPU cores
	}

	// Check CPU quota (for --cpu-quota and --cpu-period)
	if hostConfig.CPUQuota > 0 && hostConfig.CPUPeriod > 0 {
		return float64(hostConfig.CPUQuota) / float64(hostConfig.CPUPeriod)
	}

	// If limits are not set, return system core count or default value
	// For simplicity, return 0, which will mean "unlimited"
	return 0.0
}

// convertPorts converts Docker ports to our structure
func (c *dockerMetricsCollector) convertPorts(dockerPorts []types.Port) []entities.DockerPort {
	ports := make([]entities.DockerPort, 0, len(dockerPorts))
	for _, port := range dockerPorts {
		dockerPort := entities.DockerPort{
			PrivatePort: int(port.PrivatePort),
			Type:        port.Type,
		}
		if port.PublicPort > 0 {
			dockerPort.PublicPort = int(port.PublicPort)
			dockerPort.IP = port.IP
		}
		ports = append(ports, dockerPort)
	}
	return ports
}

// getContainerResourceStats gets container resource usage statistics
func (c *dockerMetricsCollector) getContainerResourceStats(ctx context.Context, cli *client.Client, containerID string, cpuLimit float64) entities.DockerStats {

	stats, err := cli.ContainerStats(ctx, containerID, false)
	if err != nil {
		return entities.DockerStats{} // Return empty statistics on error
	}
	defer stats.Body.Close()

	var containerStats types.StatsJSON
	decoder := json.NewDecoder(stats.Body)
	if err := decoder.Decode(&containerStats); err != nil {
		return entities.DockerStats{} // Return empty statistics on error
	}

	// Safely get memory statistics
	memoryUsage := containerStats.MemoryStats.Usage
	memoryLimit := containerStats.MemoryStats.Limit
	memoryPercent := float64(0)
	if memoryLimit > 0 {
		memoryPercent = float64(memoryUsage) / float64(memoryLimit) * 100.0
	}

	// Safely get network statistics
	var networkRx, networkTx uint64
	if containerStats.Networks != nil {
		for _, network := range containerStats.Networks {
			networkRx += network.RxBytes
			networkTx += network.TxBytes
		}
	}
	c.logger.Debug("Network stats for container", "container_id", containerID, "network_rx_bytes", networkRx, "network_tx_bytes", networkTx)

	// Safely get disk statistics
	var blockRead, blockWrite uint64
	if containerStats.BlkioStats.IoServiceBytesRecursive != nil {
		for _, ioStat := range containerStats.BlkioStats.IoServiceBytesRecursive {
			if ioStat.Op == "read" {
				blockRead += ioStat.Value
			} else if ioStat.Op == "write" {
				blockWrite += ioStat.Value
			}
		}
	}

	// Calculate CPU usage percentage relative to entire system
	// Add panic protection
	defer func() {
		if r := recover(); r != nil {
			fmt.Printf("Panic in CPU calculation for container %s: %v\n", containerID, r)
		}
	}()
	cpuPercent := c.calculateCPUPercentWithCache(containerID, &containerStats)

	// Calculate CPU usage percentage relative to container limit
	cpuPercentOfLimit := c.calculateCPUPercentOfLimit(&containerStats, cpuLimit)

	return entities.DockerStats{
		CPUPercent:        cpuPercent,
		CPULimit:          cpuLimit,
		CPUPercentOfLimit: cpuPercentOfLimit,
		MemoryUsage:       memoryUsage,
		MemoryLimit:       memoryLimit,
		MemoryPercent:     memoryPercent,
		NetworkRx:         networkRx,
		NetworkTx:         networkTx,
		BlockRead:         blockRead,
		BlockWrite:        blockWrite,
	}
}

// calculateCPUPercentWithCache calculates CPU usage percentage using cached previous values (thread-safe)
func (c *dockerMetricsCollector) calculateCPUPercentWithCache(containerID string, stats *types.StatsJSON) float64 {
	currentTotal := stats.CPUStats.CPUUsage.TotalUsage
	currentSystem := stats.CPUStats.SystemUsage
	currentTime := time.Now()

	// Use read lock for reading from cache
	c.cacheMutex.RLock()
	prev, exists := c.containerCPUCache[containerID]
	c.cacheMutex.RUnlock()

	if !exists {
		// First request for this container - save current values and return 0
		c.cacheMutex.Lock()
		c.containerCPUCache[containerID] = cpuStatsCache{
			totalUsage:  currentTotal,
			systemUsage: currentSystem,
			timestamp:   currentTime,
		}
		c.cacheMutex.Unlock()
		return 0.0
	}

	// Calculate time difference
	timeDelta := currentTime.Sub(prev.timestamp).Seconds()
	if timeDelta <= 0 {
		return 0.0
	}

	// Calculate CPU usage difference
	cpuDelta := float64(currentTotal) - float64(prev.totalUsage)
	systemDelta := float64(currentSystem) - float64(prev.systemUsage)

	// Update cache with write lock
	c.cacheMutex.Lock()
	c.containerCPUCache[containerID] = cpuStatsCache{
		totalUsage:  currentTotal,
		systemUsage: currentSystem,
		timestamp:   currentTime,
	}
	c.cacheMutex.Unlock()

	// Calculate CPU percentage
	// Docker TotalUsage already accounts for all system cores, so we don't multiply by core count
	if systemDelta > 0 && cpuDelta > 0 {
		return (cpuDelta / systemDelta) * 100.0
	}

	return 0.0
}

// calculateCPUPercentOfLimit calculates CPU usage percentage relative to container limit
// If limit is not set, calculates relative to total system CPU cores
func (c *dockerMetricsCollector) calculateCPUPercentOfLimit(stats *types.StatsJSON, cpuLimit float64) float64 {
	// If limit is not set, use system core count as "allocated to Docker daemon"
	actualLimit := cpuLimit
	if cpuLimit <= 0 {
		actualLimit = float64(runtime.NumCPU())
		c.logger.Debug("CPU limit not set, using system CPU cores as limit", "system_cores", actualLimit)
	}

	c.logger.Debug("Calculating CPU percent of limit", "actual_limit", actualLimit, "pre_total_usage", stats.PreCPUStats.CPUUsage.TotalUsage, "current_total_usage", stats.CPUStats.CPUUsage.TotalUsage)

	// Check if previous data exists
	if stats.PreCPUStats.CPUUsage.TotalUsage == 0 {
		// No previous data, return 0
		c.logger.Debug("No previous CPU stats, returning 0")
		return 0.0
	}

	// Calculate time difference between measurements
	timeDelta := stats.Read.Sub(stats.PreRead).Seconds()

	if timeDelta <= 0 {
		c.logger.Debug("Invalid time delta, returning 0", "time_delta", timeDelta)
		return 0.0
	}

	// Calculate container CPU usage difference
	cpuDelta := float64(stats.CPUStats.CPUUsage.TotalUsage) - float64(stats.PreCPUStats.CPUUsage.TotalUsage)

	// Calculate average CPU usage in nanoseconds per second
	cpuUsagePerSecond := cpuDelta / timeDelta

	// Convert to seconds (1 CPU core = 1000000000 nanoseconds per second)
	cpuUsageCores := cpuUsagePerSecond / 1000000000.0

	// Calculate percentage of limit
	result := (cpuUsageCores / actualLimit) * 100.0
	c.logger.Debug("CPU percent of limit calculated", "cpu_usage_cores", cpuUsageCores, "actual_limit", actualLimit, "result", result)

	return result
}

// calculateCPUPercentUnix calculates CPU usage percentage for Unix systems (deprecated function)
func (c *dockerMetricsCollector) calculateCPUPercentUnix(stats *types.StatsJSON) float64 {
	cpuPercent := 0.0

	// calculate the change for the cpu usage of the container in between readings
	cpuDelta := float64(stats.CPUStats.CPUUsage.TotalUsage) - float64(stats.PreCPUStats.CPUUsage.TotalUsage)
	// calculate the change for the entire system between readings
	systemDelta := float64(stats.CPUStats.SystemUsage) - float64(stats.PreCPUStats.SystemUsage)

	if systemDelta > 0.0 && cpuDelta > 0.0 {
		cpuPercent = (cpuDelta / systemDelta) * float64(len(stats.CPUStats.CPUUsage.PercpuUsage)) * 100.0
	}

	return cpuPercent
}

// parseContainerCreatedTime parses container creation time from string and returns string
func (c *dockerMetricsCollector) parseContainerCreatedTime(createdStr string) string {
	// Check for empty string
	if createdStr == "" {
		return time.Now().Format(time.RFC3339)
	}

	// Try to parse in RFC3339 format
	if t, err := time.Parse(time.RFC3339, createdStr); err == nil {
		return t.Format(time.RFC3339)
	}
	// Try to parse in RFC3339Nano format
	if t, err := time.Parse(time.RFC3339Nano, createdStr); err == nil {
		return t.Format(time.RFC3339)
	}
	// If failed, return current time
	return time.Now().Format(time.RFC3339)
}

// parseContainerFinishedTime parses container finished time from string and returns string
func (c *dockerMetricsCollector) parseContainerFinishedTime(finishedStr string) string {
	// Check for empty string (container is still running)
	if finishedStr == "" || finishedStr == "0001-01-01T00:00:00Z" {
		return ""
	}

	// Try to parse in RFC3339 format
	if t, err := time.Parse(time.RFC3339, finishedStr); err == nil {
		return t.Format(time.RFC3339)
	}
	// Try to parse in RFC3339Nano format
	if t, err := time.Parse(time.RFC3339Nano, finishedStr); err == nil {
		return t.Format(time.RFC3339)
	}
	// If failed, return empty string
	return ""
}
