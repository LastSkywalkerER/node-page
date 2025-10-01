package collectors

import (
	"context"
	"runtime"

	"github.com/charmbracelet/log"
	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/load"

	"system-stats/internal/modules/cpu/infrastructure/entities"
)

/**
 * cpuMetricsCollector implements the CPUMetricsCollector interface.
 * This collector gathers CPU performance statistics using cross-platform
 * system monitoring libraries (gopsutil).
 */
type CPUMetricsCollector struct {
	logger *log.Logger
}

/**
 * NewCPUMetricsCollector creates a new CPU metrics collector instance.
 * This constructor initializes the collector for gathering CPU statistics.
 *
 * @param logger The logger instance for logging collection operations
 * @return *cpuMetricsCollector Returns the initialized CPU collector
 */
func NewCPUMetricsCollector(logger *log.Logger) *CPUMetricsCollector {
	return &CPUMetricsCollector{logger: logger}
}

/**
 * CollectCPUMetrics gathers current CPU performance statistics.
 * This method collects CPU usage percentage, core count, and system load averages.
 *
 * @param ctx The context for the operation
 * @return entities.CPUMetric The collected CPU metrics
 * @return error Returns an error if CPU metrics collection fails
 */
func (c *CPUMetricsCollector) CollectCPUMetrics(ctx context.Context) (entities.CPUMetric, error) {
	c.logger.Info("Collecting CPU usage percentage")
	// Get CPU usage percentage (without delay for fast response)
	percentages, err := cpu.PercentWithContext(ctx, 0, false)
	if err != nil {
		c.logger.Error("Failed to collect CPU usage percentage", "error", err)
		return entities.CPUMetric{}, err
	}

	var usage float64
	if len(percentages) > 0 {
		usage = percentages[0]
	}

	// Get number of cores
	cores := runtime.NumCPU()

	c.logger.Info("Collecting CPU load averages")
	// Get load average
	loadStat, err := load.AvgWithContext(ctx)
	if err != nil {
		c.logger.Error("Failed to collect CPU load averages", "error", err)
		return entities.CPUMetric{}, err
	}

	c.logger.Info("CPU metrics collected successfully", "usage_percent", usage, "cores", cores)
	return entities.CPUMetric{
		UsagePercent: usage,
		Cores:        cores,
		LoadAvg1:     loadStat.Load1,
		LoadAvg5:     loadStat.Load5,
		LoadAvg15:    loadStat.Load15,
	}, nil
}
