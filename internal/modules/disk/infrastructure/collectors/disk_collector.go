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
	var primary *disk.UsageStat // root or largest non-virtual filesystem as primary
	var best *disk.UsageStat
	var bestTotal uint64

	// Prefer root filesystem totals first (reliable in VMs/containers)
	if ru, rerr := disk.UsageWithContext(ctx, "/"); rerr == nil && ru != nil {
		if ru.Total > 0 { // accept even if fstype is empty
			primary = ru
		}
	}
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
		// Track the largest non-virtual filesystem as a fallback candidate
		if !isVirtualFilesystem(u.Fstype) && u.Total > bestTotal {
			best = u
			bestTotal = u.Total
		}
	}

	// Determine totals from primary filesystem to avoid double-counting multiple mounts
	if primary != nil {
		total = primary.Total
		used = primary.Used
		free = primary.Free
	} else {
		// Prefer best candidate if available, else aggregate as last resort
		if best != nil {
			total = best.Total
			used = best.Used
			free = best.Free
		} else {
			// Fallback: aggregate (best-effort) if no suitable primary found
			for _, m := range mounts {
				total += m.Total
				used += m.Used
				free += m.Free
			}
		}
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

// isVirtualFilesystem returns true for pseudo/virtual filesystems that shouldn't
// be used for overall disk capacity calculations.
func isVirtualFilesystem(fs string) bool {
	if fs == "" {
		return true
	}
	switch strings.ToLower(fs) {
	case "tmpfs", "devtmpfs", "devfs", "proc", "sysfs", "cgroup", "cgroup2",
		"overlay", "squashfs", "autofs", "tracefs", "nsfs", "ramfs", "aufs",
		"zram", "ecryptfs", "fusectl", "fdescfs", "binder", "configfs",
		"securityfs", "pstore", "debugfs":
		return true
	}
	// Treat any fuse.* helpers (gvfs, app images, etc.) as virtual
	if strings.HasPrefix(strings.ToLower(fs), "fuse") {
		return true
	}
	// Network filesystems should not determine capacity
	switch strings.ToLower(fs) {
	case "nfs", "nfs4", "smbfs", "cifs", "afpfs", "9p":
		return true
	}
	return false
}
