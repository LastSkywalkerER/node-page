package entities

import (
	"time"
)

 // HistoricalMemoryMetric represents a historical memory usage metric stored in the database.
 // This structure contains memory utilization statistics recorded at a specific time,
 // including both percentage and absolute byte values.
type HistoricalMemoryMetric struct {
	// HostID is the foreign key referencing the host that recorded this metric
	HostID *uint `json:"host_id" gorm:"default:null;index;index:idx_mem_host_ts"`

	// Timestamp indicates when this memory metric was recorded (primary key)
	Timestamp time.Time `json:"timestamp" gorm:"primaryKey;index;index:idx_mem_host_ts"`

	// UsagePercent shows the memory utilization percentage at the time of recording
	UsagePercent float64 `json:"usage_percent" gorm:"column:usage_percent"`

	// UsedBytes shows the amount of memory used in bytes
	UsedBytes uint64 `json:"used_bytes" gorm:"column:used_bytes"`

	// TotalBytes shows the total amount of memory available in bytes
	TotalBytes uint64 `json:"total_bytes" gorm:"column:total_bytes"`
}

 // GetTimestamp returns the timestamp when this memory metric was recorded.
func (h HistoricalMemoryMetric) GetTimestamp() time.Time { return h.Timestamp }

 // GetMetricType returns the metric type identifier for memory metrics.
func (h HistoricalMemoryMetric) GetMetricType() string { return "memory" }

 // TableName returns the database table name for GORM operations.
func (HistoricalMemoryMetric) TableName() string { return "memory_metrics" }
