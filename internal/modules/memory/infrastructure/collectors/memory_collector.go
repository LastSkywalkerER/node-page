package collectors

import (
	"context"

	"github.com/charmbracelet/log"
	"github.com/shirou/gopsutil/v3/mem"

	"system-stats/internal/modules/memory/infrastructure/entities"
)

/**
 * memoryMetricsCollector implements the MemoryMetricsCollector interface.
 * This collector gathers memory performance statistics using cross-platform
 * system monitoring libraries (gopsutil).
 */
type MemoryMetricsCollector struct {
	logger *log.Logger
}

/**
 * NewMemoryMetricsCollector creates a new memory metrics collector instance.
 * This constructor initializes the collector for gathering memory statistics.
 *
 * @param logger The logger instance for logging collection operations
 * @return *memoryMetricsCollector Returns the initialized memory collector
 */
func NewMemoryMetricsCollector(logger *log.Logger) *MemoryMetricsCollector {
	return &MemoryMetricsCollector{logger: logger}
}

/**
 * CollectMemoryMetrics gathers current memory performance statistics.
 * This method collects memory usage, available memory, and usage percentages.
 *
 * @param ctx The context for the operation
 * @return entities.MemoryMetric The collected memory metrics
 * @return error Returns an error if memory metrics collection fails
 */
func (c *MemoryMetricsCollector) CollectMemoryMetrics(ctx context.Context) (entities.MemoryMetric, error) {
	c.logger.Info("Collecting memory statistics")
	memStat, err := mem.VirtualMemoryWithContext(ctx)
	if err != nil {
		c.logger.Error("Failed to collect memory statistics", "error", err)
		return entities.MemoryMetric{}, err
	}

	c.logger.Info("Memory metrics collected successfully", "total", memStat.Total, "used_percent", memStat.UsedPercent)
	return entities.MemoryMetric{
		Total:        memStat.Total,
		Available:    memStat.Available,
		Used:         memStat.Used,
		UsagePercent: memStat.UsedPercent,
		Free:         memStat.Free,
	}, nil
}
