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

// hostInitMeminfoPath is meminfo visible from PID 1's mount namespace on the host.
// Reading /host/proc/meminfo from a container often still reflects the reader's cgroup;
// this path usually yields real host RAM when /host is the host root bind-mount.
func hostInitMeminfoPath() string {
	return filepath.Join(hostProcDir(), "1/root/proc/meminfo")
}

// tryVirtualMemoryFromHostInit parses meminfo from the host init namespace when available.
func tryVirtualMemoryFromHostInit(logger *log.Logger) (entities.MemoryMetric, bool) {
	path := hostInitMeminfoPath()
	f, err := os.Open(path)
	if err != nil {
		return entities.MemoryMetric{}, false
	}
	defer f.Close()

	parsed, err := parseMeminfoKBytes(f)
	if err != nil || parsed.total == 0 {
		return entities.MemoryMetric{}, false
	}

	m := parsed.toEntity()
	if logger != nil {
		logger.Debug("Using host init namespace meminfo", "path", path, "total", m.Total, "used_percent", m.UsagePercent)
	}
	return m, true
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
