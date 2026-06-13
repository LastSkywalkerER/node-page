package memory

import (
	"time"
)

type MemoryMetric struct {
	Total        uint64  `json:"total"`
	Available    uint64  `json:"available"`
	Used         uint64  `json:"used"`
	UsagePercent float64 `json:"usage_percent"`
	Free         uint64  `json:"free"`
	Cached       uint64  `json:"cached"`
	Buffers      uint64  `json:"buffers"`
	SwapTotal    uint64  `json:"swap_total"`
	SwapUsed     uint64  `json:"swap_used"`
	Active       uint64  `json:"active"`
	Inactive     uint64  `json:"inactive"`
	Shared       uint64  `json:"shared"`
	SwapFree     uint64  `json:"swap_free"`
}

func (m MemoryMetric) GetTimestamp() time.Time { return time.Now() }
func (m MemoryMetric) GetType() string         { return "memory" }

type HistoricalMemoryMetric struct {
	HostID       *uint     `json:"host_id" gorm:"primaryKey;default:null;index;index:idx_mem_host_ts"`
	Timestamp    time.Time `json:"timestamp" gorm:"primaryKey;index;index:idx_mem_host_ts"`
	UsagePercent float64   `json:"usage_percent" gorm:"column:usage_percent"`
	UsedBytes    uint64    `json:"used_bytes" gorm:"column:used_bytes"`
	TotalBytes   uint64    `json:"total_bytes" gorm:"column:total_bytes"`
}

func (h HistoricalMemoryMetric) GetTimestamp() time.Time { return h.Timestamp }
func (h HistoricalMemoryMetric) GetMetricType() string   { return "memory" }
func (HistoricalMemoryMetric) TableName() string         { return "memory_metrics" }
