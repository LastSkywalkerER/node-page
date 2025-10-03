package entities

import (
	"time"
)

/**
 * CPUMetric represents CPU performance and utilization metrics.
 * This structure contains information about CPU usage percentage, core count,
 * system load averages over different time periods, and temperature.
 */
type CPUMetric struct {
	/** UsagePercent shows the current CPU utilization as a percentage */
	UsagePercent float64 `json:"usage_percent"`

	/** Cores indicates the total number of CPU cores available */
	Cores int `json:"cores"`

	/** LoadAvg1 represents the system load average over the last 1 minute */
	LoadAvg1 float64 `json:"load_avg_1"`

	/** LoadAvg5 represents the system load average over the last 5 minutes */
	LoadAvg5 float64 `json:"load_avg_5"`

	/** LoadAvg15 represents the system load average over the last 15 minutes */
	LoadAvg15 float64 `json:"load_avg_15"`

	/** Temperature represents the current CPU temperature in Celsius */
	Temperature float64 `json:"temperature"`
}

/**
 * GetTimestamp returns the current time for CPU metrics.
 * @return time.Time The current timestamp
 */
func (c CPUMetric) GetTimestamp() time.Time { return time.Now() }

/**
 * GetType returns the metric type identifier for CPU metrics.
 * @return string Always returns "cpu"
 */
func (c CPUMetric) GetType() string { return "cpu" }
