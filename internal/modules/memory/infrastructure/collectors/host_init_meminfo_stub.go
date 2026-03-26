//go:build !linux

package collectors

import (
	"github.com/charmbracelet/log"

	"system-stats/internal/modules/memory/infrastructure/entities"
)

func tryVirtualMemoryFromHostInit(_ *log.Logger) (entities.MemoryMetric, bool) {
	return entities.MemoryMetric{}, false
}
