package disk

import (
	"time"
)

type DiskMetric struct {
	Total        uint64         `json:"total"`
	Used         uint64         `json:"used"`
	Free         uint64         `json:"free"`
	UsagePercent float64        `json:"usage_percent"`
	Partitions   []PartitionStat `json:"partitions" gorm:"-"`
	Mounts       []UsageStat    `json:"mounts" gorm:"-"`
	IOCounters   []IOCounterStat `json:"io_counters" gorm:"-"`
}

func (d DiskMetric) GetTimestamp() time.Time { return time.Now() }
func (d DiskMetric) GetType() string         { return "disk" }

type PartitionStat struct {
	Device     string `json:"device"`
	Mountpoint string `json:"mountpoint"`
	Fstype     string `json:"fstype"`
	Opts       string `json:"opts"`
}

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

type HistoricalDiskMetric struct {
	HostID       *uint     `json:"host_id" gorm:"default:null;index;index:idx_disk_host_ts"`
	Timestamp    time.Time `json:"timestamp" gorm:"primaryKey;index;index:idx_disk_host_ts"`
	UsagePercent float64   `json:"usage_percent" gorm:"column:usage_percent"`
	UsedBytes    uint64    `json:"used_bytes" gorm:"column:used_bytes"`
	TotalBytes   uint64    `json:"total_bytes" gorm:"column:total_bytes"`
}

func (h HistoricalDiskMetric) GetTimestamp() time.Time { return h.Timestamp }
func (h HistoricalDiskMetric) GetMetricType() string   { return "disk" }
func (HistoricalDiskMetric) TableName() string         { return "disk_metrics" }
