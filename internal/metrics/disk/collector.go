package disk

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/log"
	"github.com/shirou/gopsutil/v4/disk"
)

// partitionsRefresh bounds how often the mount table is re-parsed. The
// per-mount USAGE (statfs) is still sampled every tick; only the list of
// what is mounted — near-static — is cached.
const partitionsRefresh = 60 * time.Second

// statfsTimeout is the budget for one statfs(2). A dead NFS/CIFS/FUSE mount
// parks the caller in uninterruptible sleep; without a watchdog that wedges
// the whole tick for every other metric.
const statfsTimeout = 5 * time.Second

type diskCollector struct {
	logger *log.Logger

	rootOnce sync.Once
	hostRoot string

	partMu  sync.Mutex
	parts   []disk.PartitionStat
	partsAt time.Time

	ioIdent ioIdentityCache
}

func newDiskCollector(logger *log.Logger) *diskCollector {
	return &diskCollector{logger: logger}
}

func hostDiskRoot() string {
	if r := strings.TrimSpace(os.Getenv("HOST_ROOT")); r != "" {
		return filepath.Clean(r)
	}
	if st, err := os.Stat("/host"); err == nil && st.IsDir() {
		return "/host"
	}
	return ""
}

// hostRootDir resolves the host root bind mount once; it cannot change while
// the process runs.
func (c *diskCollector) hostRootDir() string {
	c.rootOnce.Do(func() { c.hostRoot = hostDiskRoot() })
	return c.hostRoot
}

// partitions returns the mount table, re-read every partitionsRefresh.
func (c *diskCollector) partitions(ctx context.Context) []disk.PartitionStat {
	c.partMu.Lock()
	defer c.partMu.Unlock()
	if c.parts != nil && time.Since(c.partsAt) < partitionsRefresh {
		return c.parts
	}
	parts, err := disk.PartitionsWithContext(ctx, true)
	if err != nil {
		c.logger.Warn("Failed to collect partitions", "error", err)
		if c.parts != nil {
			return c.parts // keep serving the last good table
		}
		parts = []disk.PartitionStat{}
	}
	c.parts = parts
	c.partsAt = time.Now()
	return parts
}

// Pseudo filesystems whose usage is meaningless (and whose statfs is pure
// overhead): node_exporter's default exclude list plus the BSD/macOS
// equivalents. They stay in the Partitions table; they get no usage row.
var usageExcludedFSTypes = map[string]struct{}{
	"autofs": {}, "binfmt_misc": {}, "bpf": {}, "cgroup": {}, "cgroup2": {}, "configfs": {},
	"debugfs": {}, "devpts": {}, "devtmpfs": {}, "fusectl": {}, "hugetlbfs": {}, "iso9660": {},
	"mqueue": {}, "nsfs": {}, "overlay": {}, "proc": {}, "procfs": {}, "pstore": {},
	"rpc_pipefs": {}, "securityfs": {}, "selinuxfs": {}, "squashfs": {}, "erofs": {},
	"sysfs": {}, "tracefs": {}, "devfs": {}, "fdescfs": {}, "binder": {}, "efivarfs": {},
}

// usageExcludedMounts matches mount points that are kernel/container plumbing
// rather than storage (node_exporter's default).
var usageExcludedMounts = regexp.MustCompile(`^/(dev|proc|run/credentials/.+|sys|var/lib/docker/.+|var/lib/containers/storage/.+)($|/)`)

func skipUsage(p disk.PartitionStat) bool {
	if _, ok := usageExcludedFSTypes[strings.ToLower(p.Fstype)]; ok {
		return true
	}
	return usageExcludedMounts.MatchString(p.Mountpoint)
}

// stuckMounts remembers mount points whose statfs overran statfsTimeout. They
// are skipped on later ticks until the overdue call finally returns (which
// clears the entry) — the node_exporter approach.
var (
	stuckMu     sync.Mutex
	stuckMounts = make(map[string]struct{})
)

var errMountStuck = errors.New("mount point timeout")

// usageWithWatchdog runs disk.Usage behind a timer. On timeout the mount is
// marked stuck and skipped; the pending call keeps running and unmarks it
// when it completes.
func usageWithWatchdog(ctx context.Context, path string) (*disk.UsageStat, error) {
	stuckMu.Lock()
	_, stuck := stuckMounts[path]
	stuckMu.Unlock()
	if stuck {
		return nil, errMountStuck
	}
	type res struct {
		u   *disk.UsageStat
		err error
	}
	ch := make(chan res, 1)
	done := false // guarded by stuckMu
	go func() {
		u, err := disk.UsageWithContext(ctx, path)
		stuckMu.Lock()
		done = true
		delete(stuckMounts, path)
		stuckMu.Unlock()
		ch <- res{u, err}
	}()
	timer := time.NewTimer(statfsTimeout)
	defer timer.Stop()
	select {
	case r := <-ch:
		return r.u, r.err
	case <-timer.C:
		stuckMu.Lock()
		if !done {
			stuckMounts[path] = struct{}{}
		}
		stuckMu.Unlock()
		return nil, errMountStuck
	}
}

func usageStatFromDiskUsage(u *disk.UsageStat, displayPath string) UsageStat {
	return UsageStat{
		Path:              displayPath,
		Fstype:            u.Fstype,
		Total:             u.Total,
		Free:              u.Free,
		Used:              u.Used,
		UsedPercent:       u.UsedPercent,
		InodesTotal:       u.InodesTotal,
		InodesUsed:        u.InodesUsed,
		InodesFree:        u.InodesFree,
		InodesUsedPercent: u.InodesUsedPercent,
	}
}

func (c *diskCollector) Collect(ctx context.Context) (DiskMetric, error) {
	c.logger.Debug("Collecting disk usage statistics for all partitions")

	// Partitions (cached mount table)
	parts := c.partitions(ctx)
	partitions := make([]PartitionStat, 0, len(parts))
	for _, p := range parts {
		partitions = append(partitions, PartitionStat{
			Device:     p.Device,
			Mountpoint: p.Mountpoint,
			Fstype:     p.Fstype,
			Opts:       strings.Join(p.Opts, ","),
		})
	}

	// Usage per mountpoint and totals
	var total, used, free uint64
	var mounts []UsageStat
	var primary *disk.UsageStat // root or largest non-virtual filesystem as primary
	var best *disk.UsageStat
	var bestTotal uint64
	hostPrimary := false // primary totals come from host bind-mount (e.g. /host), not container /

	// Prefer host bind-mount (e.g. Docker /:/host:ro) so totals match the real machine, not the container overlay.
	if hr := c.hostRootDir(); hr != "" {
		if ru, rerr := usageWithWatchdog(ctx, hr); rerr == nil && ru != nil && ru.Total > 0 {
			primary = ru
			hostPrimary = true
			c.logger.Debug("Disk primary from host root bind", "path", hr)
		}
	}
	if primary == nil {
		if ru, rerr := usageWithWatchdog(ctx, "/"); rerr == nil && ru != nil {
			if ru.Total > 0 { // accept even if fstype is empty
				primary = ru
			}
		}
	}
	// Per-mount rows: inside Docker, partition list is the container's — paths like /, /run, /tmp
	// often resolve to the same overlay and duplicate the host-sized totals. Skip that when we
	// already use the host bind-mount for primary; expose a single row for host root instead.
	if !hostPrimary {
		seen := make(map[string]struct{}, len(parts))
		for _, p := range parts {
			// Pseudo filesystems: no statfs, no row. The same mount point
			// listed twice (bind mounts) is measured once.
			if skipUsage(p) {
				continue
			}
			if _, dup := seen[p.Mountpoint]; dup {
				continue
			}
			seen[p.Mountpoint] = struct{}{}
			u, uerr := usageWithWatchdog(ctx, p.Mountpoint)
			if uerr != nil {
				// In Docker/containers many host mounts are inaccessible — skip silently
				if errors.Is(uerr, errMountStuck) || strings.Contains(uerr.Error(), "no such file or directory") {
					continue
				}
				c.logger.Warn("Failed to collect usage for mount", "mount", p.Mountpoint, "error", uerr)
				continue
			}
			mounts = append(mounts, UsageStat{
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
	} else if primary != nil {
		mounts = []UsageStat{usageStatFromDiskUsage(primary, "/")}
		partitions = []PartitionStat{{
			Device:     "host-root",
			Mountpoint: "/",
			Fstype:     primary.Fstype,
			Opts:       "bind",
		}}
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

	// IO Counters (per-OS: Linux parses /proc/diskstats directly and caches
	// the static serial/label identity; see iocounters_*.go)
	ioMap, err := c.ioCounters(ctx)
	if err != nil {
		c.logger.Warn("Failed to collect IO counters", "error", err)
		ioMap = map[string]disk.IOCountersStat{}
	}
	ioCounters := make([]IOCounterStat, 0, len(ioMap))
	for name, io := range ioMap {
		ioCounters = append(ioCounters, IOCounterStat{
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

	c.logger.Debug("Disk metrics collected successfully", "mounts", len(mounts), "devices", len(ioCounters))
	return DiskMetric{
		Total:        total,
		Used:         used,
		Free:         free,
		UsagePercent: usagePercent,
		Partitions:   partitions,
		Mounts:       mounts,
		IOCounters:   ioCounters,
	}, nil
}

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
