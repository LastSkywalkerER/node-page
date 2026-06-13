package cpu

import (
	"time"
)

type CPUMetric struct {
	UsagePercent float64  `json:"usage_percent"`
	Cores        int      `json:"cores"`
	LoadAvg1     float64  `json:"load_avg_1"`
	LoadAvg5     float64  `json:"load_avg_5"`
	LoadAvg15    float64  `json:"load_avg_15"`
	Temperature  float64  `json:"temperature"`
	VendorID     string   `json:"vendor_id"`
	Family       string   `json:"family"`
	Model        string   `json:"model"`
	ModelName    string   `json:"model_name"`
	Mhz          float64  `json:"mhz"`
	CacheSize    int32    `json:"cache_size"`
	Flags        []string `json:"flags" gorm:"-"`
	Microcode    string   `json:"microcode"`
	User         float64  `json:"user"`
	System       float64  `json:"system"`
	Idle         float64  `json:"idle"`
	Nice         float64  `json:"nice"`
	Iowait       float64  `json:"iowait"`
	Irq          float64  `json:"irq"`
	Softirq      float64  `json:"softirq"`
	Steal        float64  `json:"steal"`
	Guest        float64  `json:"guest"`
	GuestNice    float64  `json:"guest_nice"`
}

func (c CPUMetric) GetTimestamp() time.Time { return time.Now() }
func (c CPUMetric) GetType() string         { return "cpu" }

type HistoricalCPUMetric struct {
	HostID      *uint     `json:"host_id" gorm:"primaryKey;default:null;index;index:idx_cpu_host_ts"`
	Timestamp   time.Time `json:"timestamp" gorm:"primaryKey;index;index:idx_cpu_host_ts"`
	Usage       float64   `json:"usage" gorm:"column:usage"`
	Cores       int       `json:"cores" gorm:"column:cores"`
	LoadAvg1    float64   `json:"load_avg_1" gorm:"column:load_avg_1"`
	LoadAvg5    float64   `json:"load_avg_5" gorm:"column:load_avg_5"`
	LoadAvg15   float64   `json:"load_avg_15" gorm:"column:load_avg_15"`
	Temperature float64   `json:"temperature" gorm:"column:temperature"`
}

func (h HistoricalCPUMetric) GetTimestamp() time.Time { return h.Timestamp }
func (h HistoricalCPUMetric) GetMetricType() string   { return "cpu" }
func (HistoricalCPUMetric) TableName() string         { return "cpu_metrics" }
