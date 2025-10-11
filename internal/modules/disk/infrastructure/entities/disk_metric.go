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

	// Partitions available on the system
	Partitions []PartitionStat `json:"partitions" gorm:"-"`

	// Mount usage details per mountpoint
	Mounts []UsageStat `json:"mounts" gorm:"-"`

	// Per-device IO counters
	IOCounters []IOCounterStat `json:"io_counters" gorm:"-"`
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

// PartitionStat describes a disk partition
type PartitionStat struct {
	Device     string `json:"device"`
	Mountpoint string `json:"mountpoint"`
	Fstype     string `json:"fstype"`
	Opts       string `json:"opts"`
}

// UsageStat describes usage for a given mount path
type UsageStat struct {
	Path              string  `json:"path"`
	Fstype            string  `json:"fstype"`
	Total             uint64  `json:"total"`
	Free              uint64  `json:"free"`
	Used              uint64  `json:"used"`
	UsedPercent       float64 `json:"used_percent"`
	InodesTotal       uint64  `json:"inodes_total"`
	InodesUsed        uint64  `json:"inodes_used"`
	InodesFree        uint64  `json:"inodes_free"`
	InodesUsedPercent float64 `json:"inodes_used_percent"`
}

// IOCounterStat describes IO counters for a block device
type IOCounterStat struct {
	Name             string `json:"name"`
	ReadCount        uint64 `json:"read_count"`
	MergedReadCount  uint64 `json:"merged_read_count"`
	WriteCount       uint64 `json:"write_count"`
	MergedWriteCount uint64 `json:"merged_write_count"`
	ReadBytes        uint64 `json:"read_bytes"`
	WriteBytes       uint64 `json:"write_bytes"`
	ReadTime         uint64 `json:"read_time"`
	WriteTime        uint64 `json:"write_time"`
	IopsInProgress   uint64 `json:"iops_in_progress"`
	IoTime           uint64 `json:"io_time"`
	WeightedIO       uint64 `json:"weighted_io"`
	SerialNumber     string `json:"serial_number"`
	Label            string `json:"label"`
}
