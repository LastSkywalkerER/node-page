//go:build linux

package disk

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v4/disk"
)

// gopsutil's IOCounters re-derives every device's serial number and label on
// EVERY call: two stat(2) on the device node plus two full scans of the
// /run/udev/data record per device — for values that never change. The
// counters themselves are one read of /proc/diskstats. So: parse diskstats
// here every tick (same field mapping as gopsutil, incl. the 512-byte sector
// size and skipping all-zero rows) and refresh serial/label through gopsutil
// only every ioIdentityRefresh.

const (
	sectorSize        = 512
	ioIdentityRefresh = 10 * time.Minute
)

type ioIdentityCache struct {
	mu     sync.Mutex
	at     time.Time
	serial map[string]string
	label  map[string]string
}

func hostProcPath() string {
	if v := strings.TrimSpace(os.Getenv("HOST_PROC")); v != "" {
		return filepath.Clean(v)
	}
	return "/proc"
}

func (c *diskCollector) ioCounters(ctx context.Context) (map[string]disk.IOCountersStat, error) {
	c.ioIdent.mu.Lock()
	stale := c.ioIdent.at.IsZero() || time.Since(c.ioIdent.at) >= ioIdentityRefresh
	c.ioIdent.mu.Unlock()
	if stale {
		// Full gopsutil pass (with the udev lookups) on the slow cadence — it
		// also yields this tick's counters, so no second read is needed.
		full, err := disk.IOCountersWithContext(ctx)
		if err == nil {
			c.ioIdent.mu.Lock()
			c.ioIdent.at = time.Now()
			c.ioIdent.serial = make(map[string]string, len(full))
			c.ioIdent.label = make(map[string]string, len(full))
			for name, d := range full {
				c.ioIdent.serial[name] = d.SerialNumber
				c.ioIdent.label[name] = d.Label
			}
			c.ioIdent.mu.Unlock()
			return full, nil
		}
		c.logger.Debug("Full IO counters pass failed, parsing diskstats directly", "error", err)
	}

	ret, err := readDiskstats(filepath.Join(hostProcPath(), "diskstats"))
	if err != nil {
		return nil, err
	}
	c.ioIdent.mu.Lock()
	for name, d := range ret {
		d.SerialNumber = c.ioIdent.serial[name]
		d.Label = c.ioIdent.label[name]
		ret[name] = d
	}
	c.ioIdent.mu.Unlock()
	return ret, nil
}

// readDiskstats parses /proc/diskstats into gopsutil's IOCountersStat shape.
func readDiskstats(path string) (map[string]disk.IOCountersStat, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	ret := make(map[string]disk.IOCountersStat)
	empty := disk.IOCountersStat{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 14 {
			continue // malformed / truncated row
		}
		var v [11]uint64
		ok := true
		for i := range v {
			n, perr := strconv.ParseUint(fields[3+i], 10, 64)
			if perr != nil {
				ok = false
				break
			}
			v[i] = n
		}
		if !ok {
			continue
		}
		d := disk.IOCountersStat{
			ReadCount:        v[0],
			MergedReadCount:  v[1],
			ReadBytes:        v[2] * sectorSize,
			ReadTime:         v[3],
			WriteCount:       v[4],
			MergedWriteCount: v[5],
			WriteBytes:       v[6] * sectorSize,
			WriteTime:        v[7],
			IopsInProgress:   v[8],
			IoTime:           v[9],
			WeightedIO:       v[10],
		}
		if d == empty {
			continue // never-touched device (gopsutil skips these too)
		}
		d.Name = fields[2]
		ret[d.Name] = d
	}
	return ret, sc.Err()
}
