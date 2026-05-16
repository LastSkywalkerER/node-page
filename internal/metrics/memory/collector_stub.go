//go:build !linux

package memory

import (
	"github.com/charmbracelet/log"
)

func tryVirtualMemoryFromHostInit(_ *log.Logger) (MemoryMetric, bool) {
	return MemoryMetric{}, false
}
