package collectors

import (
	"context"
	"strings"

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
	c.logger.Info("Collecting disk usage statistics for all partitions")

	// Partitions
	parts, err := disk.PartitionsWithContext(ctx, true)
	if err != nil {
		c.logger.Warn("Failed to collect partitions", "error", err)
		parts = []disk.PartitionStat{}
	}
	partitions := make([]entities.PartitionStat, 0, len(parts))
	for _, p := range parts {
		partitions = append(partitions, entities.PartitionStat{
			Device:     p.Device,
			Mountpoint: p.Mountpoint,
			Fstype:     p.Fstype,
			Opts:       strings.Join(p.Opts, ","),
		})
	}

	// Usage per mountpoint and totals
	var total, used, free uint64
	var mounts []entities.UsageStat
	for _, p := range parts {
		u, uerr := disk.UsageWithContext(ctx, p.Mountpoint)
		if uerr != nil {
			c.logger.Warn("Failed to collect usage for mount", "mount", p.Mountpoint, "error", uerr)
			continue
		}
		mounts = append(mounts, entities.UsageStat{
			Path:              u.Path,
			Fstype:            u.Fstype,
			Total:             u.Total,
			Free:              u.Free,
			Used:              u.Used,
			UsedPercent:       u.UsedPercent,
			InodesTotal:       u.InodesTotal,
			InodesUsed:        u.InodesUsed,
			InodesFree:        u.InodesFree,
			InodesUsedPercent: u.InodesUsedPercent,
		})
		total += u.Total
		used += u.Used
		free += u.Free
	}

	// IO Counters
	ioMap, err := disk.IOCountersWithContext(ctx)
	if err != nil {
		c.logger.Warn("Failed to collect IO counters", "error", err)
		ioMap = map[string]disk.IOCountersStat{}
	}
	ioCounters := make([]entities.IOCounterStat, 0, len(ioMap))
	for name, io := range ioMap {
		ioCounters = append(ioCounters, entities.IOCounterStat{
			Name:             name,
			ReadCount:        io.ReadCount,
			MergedReadCount:  io.MergedReadCount,
			WriteCount:       io.WriteCount,
			MergedWriteCount: io.MergedWriteCount,
			ReadBytes:        io.ReadBytes,
			WriteBytes:       io.WriteBytes,
			ReadTime:         io.ReadTime,
			WriteTime:        io.WriteTime,
			IopsInProgress:   io.IopsInProgress,
			IoTime:           io.IoTime,
			WeightedIO:       io.WeightedIO,
			SerialNumber:     io.SerialNumber,
			Label:            io.Label,
		})
	}

	var usagePercent float64
	if total > 0 {
		usagePercent = (float64(used) / float64(total)) * 100.0
	}

	c.logger.Info("Disk metrics collected successfully", "mounts", len(mounts), "devices", len(ioCounters))
	return entities.DiskMetric{
		Total:        total,
		Used:         used,
		Free:         free,
		UsagePercent: usagePercent,
		Partitions:   partitions,
		Mounts:       mounts,
		IOCounters:   ioCounters,
	}, nil
}
