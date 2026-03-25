package entities

import (
	"time"
)

 // HistoricalNetworkMetric represents a historical network metric stored in the database.
 // This structure contains complete network interface statistics recorded at a specific time.
type HistoricalNetworkMetric struct {
	// HostID is the foreign key referencing the host that recorded this metric
	HostID *uint `json:"host_id" gorm:"default:null;index;index:idx_net_host_ts"`

	// Timestamp indicates when this network metric was recorded (primary key)
	Timestamp time.Time `json:"timestamp" gorm:"primaryKey;index;index:idx_net_host_ts"`

	// Interfaces contains metrics for each network interface at this timestamp
	Interfaces []NetworkInterface `json:"interfaces" gorm:"serializer:json"`
}

 // GetTimestamp returns the timestamp when this network metric was recorded.
func (h HistoricalNetworkMetric) GetTimestamp() time.Time { return h.Timestamp }

 // GetMetricType returns the metric type identifier for network metrics.
func (h HistoricalNetworkMetric) GetMetricType() string { return "network" }

 // TableName returns the database table name for GORM operations.
func (HistoricalNetworkMetric) TableName() string { return "network_metrics" }
