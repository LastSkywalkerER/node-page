package entities

import (
	"time"
)

/**
 * HistoricalCPUMetric represents a historical CPU performance metric stored in the database.
 * This structure contains CPU usage statistics and system load averages recorded at a specific time,
 * used for trend analysis and historical reporting.
 */
type HistoricalCPUMetric struct {
	/** Timestamp indicates when this CPU metric was recorded (primary key) */
	Timestamp time.Time `json:"timestamp" gorm:"primaryKey"`

	/** Usage shows the CPU utilization percentage at the time of recording */
	Usage float64 `json:"usage" gorm:"column:usage"`

	/** Cores indicates the total number of CPU cores available at the time of recording */
	Cores int `json:"cores" gorm:"column:cores"`

	/** LoadAvg1 represents the 1-minute system load average */
	LoadAvg1 float64 `json:"load_avg_1" gorm:"column:load_avg_1"`

	/** LoadAvg5 represents the 5-minute system load average */
	LoadAvg5 float64 `json:"load_avg_5" gorm:"column:load_avg_5"`

	/** LoadAvg15 represents the 15-minute system load average */
	LoadAvg15 float64 `json:"load_avg_15" gorm:"column:load_avg_15"`
}

/**
 * GetTimestamp returns the timestamp when this CPU metric was recorded.
 * @return time.Time The recording timestamp
 */
func (h HistoricalCPUMetric) GetTimestamp() time.Time { return h.Timestamp }

/**
 * GetMetricType returns the metric type identifier for CPU metrics.
 * @return string Always returns "cpu"
 */
func (h HistoricalCPUMetric) GetMetricType() string { return "cpu" }

/**
 * TableName returns the database table name for GORM operations.
 * @return string The table name "cpu_metrics"
 */
func (HistoricalCPUMetric) TableName() string { return "cpu_metrics" }
