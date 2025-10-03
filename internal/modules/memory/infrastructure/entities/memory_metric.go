package entities

import (
	"time"
)

/**
 * MemoryMetric represents system memory (RAM) utilization metrics.
 * This structure provides information about total, used, and available memory
 * along with calculated usage percentages.
 */
type MemoryMetric struct {
	/** Total shows the total amount of physical memory in bytes */
	Total uint64 `json:"total"`

	/** Available indicates the amount of memory available for new processes in bytes */
	Available uint64 `json:"available"`

	/** Used shows the amount of memory currently in use in bytes */
	Used uint64 `json:"used"`

	/** UsagePercent shows memory utilization as a percentage */
	UsagePercent float64 `json:"usage_percent"`

	/** Free indicates the amount of completely unused memory in bytes */
	Free uint64 `json:"free"`

	/** Cached shows the amount of memory used for caching in bytes */
	Cached uint64 `json:"cached"`

	/** Buffers shows the amount of memory used for buffers in bytes */
	Buffers uint64 `json:"buffers"`

	/** SwapTotal shows the total amount of swap space in bytes */
	SwapTotal uint64 `json:"swap_total"`

	/** SwapUsed shows the amount of swap space currently in use in bytes */
	SwapUsed uint64 `json:"swap_used"`
}

/**
 * GetTimestamp returns the current time for memory metrics.
 * @return time.Time The current timestamp
 */
func (m MemoryMetric) GetTimestamp() time.Time { return time.Now() }

/**
 * GetType returns the metric type identifier for memory metrics.
 * @return string Always returns "memory"
 */
func (m MemoryMetric) GetType() string { return "memory" }
