package entities

import (
	"time"
)

/**
 * DiskMetric represents disk storage utilization metrics.
 * This structure contains information about disk space usage and availability
 * for the system's storage devices.
 */
type DiskMetric struct {
	/** Total shows the total disk space in bytes */
	Total uint64 `json:"total"`

	/** Used shows the amount of disk space currently used in bytes */
	Used uint64 `json:"used"`

	/** Free shows the amount of available disk space in bytes */
	Free uint64 `json:"free"`

	/** UsagePercent shows disk utilization as a percentage */
	UsagePercent float64 `json:"usage_percent"`
}

/**
 * GetTimestamp returns the current time for disk metrics.
 * @return time.Time The current timestamp
 */
func (d DiskMetric) GetTimestamp() time.Time { return time.Now() }

/**
 * GetType returns the metric type identifier for disk metrics.
 * @return string Always returns "disk"
 */
func (d DiskMetric) GetType() string { return "disk" }
