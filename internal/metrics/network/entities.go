package network

import (
	"time"
)

type NetworkMetric struct {
	Interfaces []NetworkInterface `json:"interfaces"`
}

func (n NetworkMetric) GetTimestamp() time.Time { return time.Now() }
func (n NetworkMetric) GetType() string         { return "network" }

type NetworkInterface struct {
	Name          string   `json:"name"`
	IPs           []string `json:"ips"`
	Mac           string   `json:"mac"`
	BytesSent     uint64   `json:"bytes_sent"`
	BytesRecv     uint64   `json:"bytes_recv"`
	PacketsSent   uint64   `json:"packets_sent"`
	PacketsRecv   uint64   `json:"packets_recv"`
	SpeedKbpsSent float64  `json:"speed_kbps_sent"`
	SpeedKbpsRecv float64  `json:"speed_kbps_recv"`
	IsPrimary     bool     `json:"is_primary"`
	Errin         uint64   `json:"errin"`
	Errout        uint64   `json:"errout"`
	Dropin        uint64   `json:"dropin"`
	Dropout       uint64   `json:"dropout"`
}

type HistoricalNetworkMetric struct {
	HostID     *uint              `json:"host_id" gorm:"default:null;index;index:idx_net_host_ts"`
	Timestamp  time.Time          `json:"timestamp" gorm:"primaryKey;index;index:idx_net_host_ts"`
	Interfaces []NetworkInterface `json:"interfaces" gorm:"serializer:json"`
}

func (h HistoricalNetworkMetric) GetTimestamp() time.Time { return h.Timestamp }
func (h HistoricalNetworkMetric) GetMetricType() string   { return "network" }
func (HistoricalNetworkMetric) TableName() string         { return "network_metrics" }
