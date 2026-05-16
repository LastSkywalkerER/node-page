package memory

import (
	"context"

	"github.com/charmbracelet/log"
	"github.com/shirou/gopsutil/v4/mem"
)

type memoryCollector struct {
	logger *log.Logger
}

func newMemoryCollector(logger *log.Logger) *memoryCollector {
	return &memoryCollector{logger: logger}
}

func (c *memoryCollector) Collect(ctx context.Context) (MemoryMetric, error) {
	c.logger.Debug("Collecting memory statistics")

	if m, ok := tryVirtualMemoryFromHostInit(c.logger); ok {
		return m, nil
	}

	memStat, err := mem.VirtualMemoryWithContext(ctx)
	if err != nil {
		c.logger.Error("Failed to collect memory statistics", "error", err)
		return MemoryMetric{}, err
	}

	swapStat, err := mem.SwapMemoryWithContext(ctx)
	if err != nil {
		c.logger.Warn("Failed to collect swap statistics", "error", err)
		swapStat = &mem.SwapMemoryStat{}
	}

	c.logger.Debug("Memory metrics collected successfully", "total", memStat.Total, "used_percent", memStat.UsedPercent)
	return MemoryMetric{
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
