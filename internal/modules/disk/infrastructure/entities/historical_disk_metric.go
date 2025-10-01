package entities

import (
	"time"
)

/**
 * HistoricalDiskMetric represents a historical disk usage metric stored in the database.
 * This structure contains disk space utilization statistics recorded at a specific time,
 * including both percentage and absolute byte values.
 */
type HistoricalDiskMetric struct {
	/** Timestamp indicates when this disk metric was recorded (primary key) */
	Timestamp time.Time `json:"timestamp" gorm:"primaryKey"`

	/** UsagePercent shows the disk utilization percentage at the time of recording */
	UsagePercent float64 `json:"usage_percent" gorm:"column:usage_percent"`

	/** UsedBytes shows the amount of disk space used in bytes */
	UsedBytes uint64 `json:"used_bytes" gorm:"column:used_bytes"`

	/** TotalBytes shows the total amount of disk space available in bytes */
	TotalBytes uint64 `json:"total_bytes" gorm:"column:total_bytes"`
}

/**
 * GetTimestamp returns the timestamp when this disk metric was recorded.
 * @return time.Time The recording timestamp
 */
func (h HistoricalDiskMetric) GetTimestamp() time.Time { return h.Timestamp }

/**
 * GetMetricType returns the metric type identifier for disk metrics.
 * @return string Always returns "disk"
 */
func (h HistoricalDiskMetric) GetMetricType() string { return "disk" }

/**
 * TableName returns the database table name for GORM operations.
 * @return string The table name "disk_metrics"
 */
func (HistoricalDiskMetric) TableName() string { return "disk_metrics" }
