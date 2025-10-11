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

	// --- CPU static/info fields (from gopsutil cpu.InfoStat aggregated) ---
	/** VendorID is the CPU vendor identifier */
	VendorID string `json:"vendor_id"`

	/** Family is the CPU family */
	Family string `json:"family"`

	/** Model is the CPU model */
	Model string `json:"model"`

	/** ModelName is the human-readable CPU model name */
	ModelName string `json:"model_name"`

	/** Mhz is the reported base clock frequency in MHz */
	Mhz float64 `json:"mhz"`

	/** CacheSize is the CPU cache size in KB */
	CacheSize int32 `json:"cache_size"`

	/** Flags are CPU feature flags */
	Flags []string `json:"flags" gorm:"-"`

	/** Microcode version string */
	Microcode string `json:"microcode"`

	// --- CPU times (aggregate across all CPUs) ---
	User      float64 `json:"user"`
	System    float64 `json:"system"`
	Idle      float64 `json:"idle"`
	Nice      float64 `json:"nice"`
	Iowait    float64 `json:"iowait"`
	Irq       float64 `json:"irq"`
	Softirq   float64 `json:"softirq"`
	Steal     float64 `json:"steal"`
	Guest     float64 `json:"guest"`
	GuestNice float64 `json:"guest_nice"`
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
