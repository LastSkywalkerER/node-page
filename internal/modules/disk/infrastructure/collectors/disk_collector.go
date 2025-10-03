package collectors

import (
	"context"

	"github.com/charmbracelet/log"
	"github.com/shirou/gopsutil/v4/disk"

	"system-stats/internal/modules/disk/infrastructure/entities"
)

/**
 * diskMetricsCollector implements the DiskMetricsCollector interface.
 * This collector gathers disk performance statistics using cross-platform
 * system monitoring libraries (gopsutil).
 */
type DiskMetricsCollector struct {
	logger *log.Logger
}

/**
 * NewDiskMetricsCollector creates a new disk metrics collector instance.
 * This constructor initializes the collector for gathering disk statistics.
 *
 * @param logger The logger instance for logging collection operations
 * @return *diskMetricsCollector Returns the initialized disk collector
 */
func NewDiskMetricsCollector(logger *log.Logger) *DiskMetricsCollector {
	return &DiskMetricsCollector{logger: logger}
}

/**
 * CollectDiskMetrics gathers current disk performance statistics.
 * This method collects disk usage, free space, and usage percentages for the root filesystem.
 *
 * @param ctx The context for the operation
 * @return entities.DiskMetric The collected disk metrics
 * @return error Returns an error if disk metrics collection fails
 */
func (c *DiskMetricsCollector) CollectDiskMetrics(ctx context.Context) (entities.DiskMetric, error) {
	c.logger.Info("Collecting disk usage statistics for root filesystem")
	// Get information about root disk
	diskStat, err := disk.UsageWithContext(ctx, "/")
	if err != nil {
		c.logger.Error("Failed to collect disk usage statistics", "error", err)
		return entities.DiskMetric{}, err
	}

	c.logger.Info("Disk metrics collected successfully", "total", diskStat.Total, "used_percent", diskStat.UsedPercent)
	return entities.DiskMetric{
		Total:        diskStat.Total,
		Used:         diskStat.Used,
		Free:         diskStat.Free,
		UsagePercent: diskStat.UsedPercent,
	}, nil
}
