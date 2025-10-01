package collectors

import (
	"context"
	"time"

	cpucollectors "system-stats/internal/modules/cpu/infrastructure/collectors"
	cpumetric "system-stats/internal/modules/cpu/infrastructure/entities"
	diskcollectors "system-stats/internal/modules/disk/infrastructure/collectors"
	diskmetric "system-stats/internal/modules/disk/infrastructure/entities"
	dockermetric "system-stats/internal/modules/docker/infrastructure/entities"
	memorycollectors "system-stats/internal/modules/memory/infrastructure/collectors"
	memorymetric "system-stats/internal/modules/memory/infrastructure/entities"
	networkcollectors "system-stats/internal/modules/network/infrastructure/collectors"
	networkmetric "system-stats/internal/modules/network/infrastructure/entities"
	systemmetric "system-stats/internal/modules/system/infrastructure/entities"

	"github.com/charmbracelet/log"
)

/**
 * systemMetricsCollector implements the SystemMetricsCollector interface.
 * This collector gathers system performance metrics using individual collectors
 * for CPU, memory, disk, and network metrics.
 */
type systemMetricsCollector struct {
	logger           *log.Logger
	cpuCollector     *cpucollectors.CPUMetricsCollector
	memoryCollector  *memorycollectors.MemoryMetricsCollector
	diskCollector    *diskcollectors.DiskMetricsCollector
	networkCollector *networkcollectors.NetworkMetricsCollector
}

/**
 * NewSystemMetricsCollector creates a new system metrics collector instance.
 * This constructor initializes the collector with individual metric collectors
 * for gathering comprehensive system statistics.
 *
 * @param logger The logger instance for logging collection operations
 * @return *systemMetricsCollector Returns the initialized system collector
 */
func NewSystemMetricsCollector(logger *log.Logger) *systemMetricsCollector {
	return &systemMetricsCollector{
		logger:           logger,
		cpuCollector:     cpucollectors.NewCPUMetricsCollector(logger),
		memoryCollector:  memorycollectors.NewMemoryMetricsCollector(logger),
		diskCollector:    diskcollectors.NewDiskMetricsCollector(logger),
		networkCollector: networkcollectors.NewNetworkMetricsCollector(logger),
	}
}

/**
 * CollectCPUMetrics gathers current CPU performance statistics.
 * This method delegates to the CPU collector for specialized CPU metrics collection.
 *
 * @param ctx The context for the operation
 * @return cpumetric.CPUMetric The collected CPU metrics
 * @return error Returns an error if CPU metrics collection fails
 */
func (c *systemMetricsCollector) CollectCPUMetrics(ctx context.Context) (cpumetric.CPUMetric, error) {
	return c.cpuCollector.CollectCPUMetrics(ctx)
}

/**
 * CollectMemoryMetrics gathers current memory usage statistics.
 * This method delegates to the memory collector for specialized memory metrics collection.
 *
 * @param ctx The context for the operation
 * @return memorymetric.MemoryMetric The collected memory metrics
 * @return error Returns an error if memory metrics collection fails
 */
func (c *systemMetricsCollector) CollectMemoryMetrics(ctx context.Context) (memorymetric.MemoryMetric, error) {
	return c.memoryCollector.CollectMemoryMetrics(ctx)
}

/**
 * CollectDiskMetrics gathers current disk storage statistics.
 * This method delegates to the disk collector for specialized disk metrics collection.
 *
 * @param ctx The context for the operation
 * @return diskmetric.DiskMetric The collected disk metrics
 * @return error Returns an error if disk metrics collection fails
 */
func (c *systemMetricsCollector) CollectDiskMetrics(ctx context.Context) (diskmetric.DiskMetric, error) {
	return c.diskCollector.CollectDiskMetrics(ctx)
}

/**
 * CollectNetworkMetrics gathers current network interface statistics.
 * This method delegates to the network collector for specialized network metrics collection.
 *
 * @param ctx The context for the operation
 * @return networkmetric.NetworkMetric The collected network metrics
 * @return error Returns an error if network metrics collection fails
 */
func (c *systemMetricsCollector) CollectNetworkMetrics(ctx context.Context) (networkmetric.NetworkMetric, error) {
	return c.networkCollector.CollectNetworkMetrics(ctx)
}

/**
 * CollectAllMetrics gathers all system performance metrics.
 * This method collects CPU, memory, disk, and network metrics using
 * individual specialized collectors.
 *
 * @param ctx The context for the operation
 * @return *systemmetric.SystemMetric The collected system metrics
 * @return error Returns an error if any metrics collection fails
 */
func (c *systemMetricsCollector) CollectAllMetrics(ctx context.Context) (*systemmetric.SystemMetric, error) {
	// Collect CPU metrics
	cpuMetrics, err := c.cpuCollector.CollectCPUMetrics(ctx)
	if err != nil {
		return nil, err
	}

	// Collect Memory metrics
	memoryMetrics, err := c.memoryCollector.CollectMemoryMetrics(ctx)
	if err != nil {
		return nil, err
	}

	// Collect Disk metrics
	diskMetrics, err := c.diskCollector.CollectDiskMetrics(ctx)
	if err != nil {
		return nil, err
	}

	// Collect Network metrics
	networkMetrics, err := c.networkCollector.CollectNetworkMetrics(ctx)
	if err != nil {
		return nil, err
	}

	return &systemmetric.SystemMetric{
		Timestamp: time.Now(),
		CPU:       cpuMetrics,
		Memory:    memoryMetrics,
		Disk:      diskMetrics,
		Network:   networkMetrics,
		Docker:    dockermetric.DockerMetric{}, // Docker metrics are collected separately
	}, nil
}
