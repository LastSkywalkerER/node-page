package collectors

import (
	"context"

	"github.com/charmbracelet/log"
	"github.com/shirou/gopsutil/v4/mem"

	"system-stats/internal/modules/memory/infrastructure/entities"
)

 // memoryMetricsCollector implements the MemoryMetricsCollector interface.
 // This collector gathers memory performance statistics using cross-platform
 // system monitoring libraries (gopsutil).
type MemoryMetricsCollector struct {
	logger *log.Logger
}

 // NewMemoryMetricsCollector creates a new memory metrics collector instance.
 // This constructor initializes the collector for gathering memory statistics.
func NewMemoryMetricsCollector(logger *log.Logger) *MemoryMetricsCollector {
	return &MemoryMetricsCollector{logger: logger}
}

 // CollectMemoryMetrics gathers current memory performance statistics.
 // This method collects memory usage, available memory, usage percentages,
 // cached memory, buffers, and swap information.
func (c *MemoryMetricsCollector) CollectMemoryMetrics(ctx context.Context) (entities.MemoryMetric, error) {
	c.logger.Debug("Collecting memory statistics")

	// Docker + /host: /host/proc/meminfo is often still cgroup-scoped for the reader; meminfo via
	// host PID 1's mount namespace matches real host RAM (see host_init_meminfo_linux.go).
	if m, ok := tryVirtualMemoryFromHostInit(c.logger); ok {
		return m, nil
	}

	memStat, err := mem.VirtualMemoryWithContext(ctx)
	if err != nil {
		c.logger.Error("Failed to collect memory statistics", "error", err)
		return entities.MemoryMetric{}, err
	}

	// Collect swap information
	swapStat, err := mem.SwapMemoryWithContext(ctx)
	if err != nil {
		c.logger.Warn("Failed to collect swap statistics", "error", err)
		// Continue without swap data
		swapStat = &mem.SwapMemoryStat{}
	}

	c.logger.Debug("Memory metrics collected successfully", "total", memStat.Total, "used_percent", memStat.UsedPercent)
	return entities.MemoryMetric{
		Total:        memStat.Total,
		Available:    memStat.Available,
		Used:         memStat.Used,
		UsagePercent: memStat.UsedPercent,
		Free:         memStat.Free,
		Cached:       memStat.Cached,
		Buffers:      memStat.Buffers,
		SwapTotal:    swapStat.Total,
		SwapUsed:     swapStat.Used,
		Active:       memStat.Active,
		Inactive:     memStat.Inactive,
		Shared:       memStat.Shared,
		SwapFree:     swapStat.Free,
	}, nil
}
