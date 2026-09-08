//go:build !linux

package disk

import (
	"context"

	"github.com/shirou/gopsutil/v4/disk"
)

// ioIdentityCache is Linux-only (serial/label come from udev there); on other
// platforms gopsutil's IOCounters is the whole story.
type ioIdentityCache struct{}

func (c *diskCollector) ioCounters(ctx context.Context) (map[string]disk.IOCountersStat, error) {
	return disk.IOCountersWithContext(ctx)
}
