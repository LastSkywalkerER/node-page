//go:build linux

package collectors

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/charmbracelet/log"

	"system-stats/internal/modules/memory/infrastructure/entities"
)

// hostProcDir returns the proc path used for host metrics (HOST_PROC or /host/proc).
func hostProcDir() string {
	p := strings.TrimSpace(os.Getenv("HOST_PROC"))
	if p != "" {
		return filepath.Clean(p)
	}
	if st, err := os.Stat("/host/proc"); err == nil && st.IsDir() {
		return "/host/proc"
	}
	return "/proc"
}

// hostRootDir returns the host root bind mount (HOST_ROOT or /host when present).
func hostRootDir() string {
	r := strings.TrimSpace(os.Getenv("HOST_ROOT"))
	if r != "" {
		return filepath.Clean(r)
	}
	if st, err := os.Stat("/host"); err == nil && st.IsDir() {
		return "/host"
	}
	return ""
}

// hostSysDir returns sysfs for host RAM discovery (HOST_SYS, else hostRoot/sys, else /host/sys, else /sys).
func hostSysDir() string {
	s := strings.TrimSpace(os.Getenv("HOST_SYS"))
	if s != "" {
		return filepath.Clean(s)
	}
	if hr := hostRootDir(); hr != "" {
		p := filepath.Join(hr, "sys")
		if st, err := os.Stat(p); err == nil && st.IsDir() {
			return p
		}
	}
	if st, err := os.Stat("/host/sys"); err == nil && st.IsDir() {
		return "/host/sys"
	}
	return "/sys"
}

// hostInitMeminfoPath is meminfo visible from PID 1's mount namespace on the host.
// Reading /host/proc/meminfo from a container often still reflects the reader's cgroup;
// this path usually yields real host RAM when /host is the host root bind-mount.
func hostInitMeminfoPath() string {
	return filepath.Join(hostProcDir(), "1/root/proc/meminfo")
}

// readSysfsOnlineRAMBytes returns total online RAM from sysfs memory blocks (host view when hostSys is /host/sys).
func readSysfsOnlineRAMBytes(hostSys string) (uint64, bool) {
	base := filepath.Join(hostSys, "devices/system/memory")
	bsData, err := os.ReadFile(filepath.Join(base, "block_size_bytes"))
	if err != nil {
		return 0, false
	}
	blockSize, err := strconv.ParseUint(strings.TrimSpace(string(bsData)), 0, 64)
	if err != nil || blockSize == 0 {
		return 0, false
	}
	entries, err := os.ReadDir(base)
	if err != nil {
		return 0, false
	}
	var onlineBlocks uint64
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), "memory") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(base, e.Name(), "online"))
		if err != nil {
			continue
		}
		if strings.TrimSpace(string(b)) == "1" {
			onlineBlocks++
		}
	}
	if onlineBlocks == 0 {
		return 0, false
	}
	return blockSize * onlineBlocks, true
}

// readHostCgroupV2MemoryCurrent reads root unified cgroup v2 memory.current under the host root bind mount.
func readHostCgroupV2MemoryCurrent(hostRoot string) (uint64, bool) {
	if hostRoot == "" {
		return 0, false
	}
	p := filepath.Join(hostRoot, "sys/fs/cgroup/memory.current")
	b, err := os.ReadFile(p)
	if err != nil {
		return 0, false
	}
	v, err := strconv.ParseUint(strings.TrimSpace(string(b)), 10, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// tryVirtualMemoryFromHostInit prefers host-accurate RAM: sysfs total + cgroup v2 root usage when meminfo MemTotal
// looks like a cgroup cap (common under Docker in LXC). Otherwise uses PID 1 mount-ns meminfo when plausible.
func tryVirtualMemoryFromHostInit(logger *log.Logger) (entities.MemoryMetric, bool) {
	hostRoot := hostRootDir()
	hostSys := hostSysDir()
	phys, physOK := readSysfsOnlineRAMBytes(hostSys)

	path := hostInitMeminfoPath()
	f, err := os.Open(path)
	if err == nil {
		parsed, perr := parseMeminfoKBytes(f)
		_ = f.Close()
		if perr == nil && parsed.total > 0 {
			cgroupLike := physOK && phys > 0 && parsed.total < phys*85/100
			if !cgroupLike {
				m := parsed.toEntity()
				if logger != nil {
					logger.Debug("Using host init namespace meminfo", "path", path, "total", m.Total, "used_percent", m.UsagePercent)
				}
				return m, true
			}
		}
	}

	used, cgOK := readHostCgroupV2MemoryCurrent(hostRoot)
	if physOK && cgOK && used <= phys {
		m := memoryMetricFromSysfsAndCgroup(phys, used, path)
		if logger != nil {
			logger.Debug("Using sysfs RAM total + host cgroup v2 memory.current", "phys", phys, "used", used, "meminfo_path", path, "host_sys", hostSys, "host_root", hostRoot)
		}
		return m, true
	}

	return entities.MemoryMetric{}, false
}

// memoryMetricFromSysfsAndCgroup builds metrics when total comes from sysfs and used from cgroup root.
// Swap lines are taken from init meminfo when readable (usually system-wide).
func memoryMetricFromSysfsAndCgroup(phys, used uint64, initMeminfoPath string) entities.MemoryMetric {
	var swapTotal, swapFree uint64
	if f, err := os.Open(initMeminfoPath); err == nil {
		if p, err := parseMeminfoKBytes(f); err == nil {
			swapTotal, swapFree = p.swapTotal, p.swapFree
		}
		_ = f.Close()
	}
	swapUsed := uint64(0)
	if swapTotal > swapFree {
		swapUsed = swapTotal - swapFree
	}
	avail := uint64(0)
	if phys > used {
		avail = phys - used
	}
	var usedPct float64
	if phys > 0 {
		usedPct = float64(used) / float64(phys) * 100.0
	}
	return entities.MemoryMetric{
		Total:         phys,
		Available:   avail,
		Used:        used,
		UsagePercent: usedPct,
		Free:        0,
		Cached:      0,
		Buffers:     0,
		SwapTotal:   swapTotal,
		SwapUsed:    swapUsed,
		SwapFree:    swapFree,
	}
}

// parseMeminfoKBytes parses /proc/meminfo-style lines (values in kB).
type meminfoKBParsed struct {
	total, available, free, cached, buffers uint64
	sreclaimable                          uint64
	active, inactive, shared              uint64
	swapTotal, swapFree                   uint64
	memAvail                              bool
}

func parseMeminfoKBytes(r interface{ Read([]byte) (int, error) }) (meminfoKBParsed, error) {
	var out meminfoKBParsed
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := sc.Text()
		i := strings.IndexByte(line, ':')
		if i < 0 {
			continue
		}
		key := strings.TrimSpace(line[:i])
		rest := strings.TrimSpace(line[i+1:])
		rest = strings.TrimSuffix(rest, " kB")
		fields := strings.Fields(rest)
		if len(fields) == 0 {
			continue
		}
		val, err := strconv.ParseUint(fields[0], 10, 64)
		if err != nil {
			continue
		}
		kb := val * 1024
		switch key {
		case "MemTotal":
			out.total = kb
		case "MemFree":
			out.free = kb
		case "MemAvailable":
			out.available = kb
			out.memAvail = true
		case "Buffers":
			out.buffers = kb
		case "Cached":
			out.cached = kb
		case "SReclaimable":
			out.sreclaimable = kb
		case "Active":
			out.active = kb
		case "Inactive":
			out.inactive = kb
		case "Shmem":
			out.shared = kb
		case "SwapTotal":
			out.swapTotal = kb
		case "SwapFree":
			out.swapFree = kb
		}
	}
	return out, sc.Err()
}

func (p meminfoKBParsed) toEntity() entities.MemoryMetric {
	// Match gopsutil/linux: reclaimable slab counts as cache for used-memory math.
	cached := p.cached + p.sreclaimable
	used := p.total - p.free - p.buffers - cached
	var avail uint64
	if p.memAvail {
		avail = p.available
	} else {
		avail = cached + p.free
	}
	var usedPct float64
	if p.total > 0 {
		usedPct = float64(used) / float64(p.total) * 100.0
	}
	swapUsed := uint64(0)
	if p.swapTotal > p.swapFree {
		swapUsed = p.swapTotal - p.swapFree
	}
	return entities.MemoryMetric{
		Total:         p.total,
		Available:   avail,
		Used:        used,
		UsagePercent: usedPct,
		Free:        p.free,
		Cached:      cached,
		Buffers:     p.buffers,
		SwapTotal:   p.swapTotal,
		SwapUsed:    swapUsed,
		Active:      p.active,
		Inactive:    p.inactive,
		Shared:      p.shared,
		SwapFree:    p.swapFree,
	}
}
