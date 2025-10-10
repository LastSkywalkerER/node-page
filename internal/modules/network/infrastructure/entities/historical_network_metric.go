package entities

import (
	"time"
)

/**
 * HistoricalNetworkMetric represents a historical network metric stored in the database.
 * This structure contains complete network interface statistics recorded at a specific time.
 */
type HistoricalNetworkMetric struct {
	/** HostID is the foreign key referencing the host that recorded this metric */
	HostID *uint `json:"host_id" gorm:"default:null"`

	/** Timestamp indicates when this network metric was recorded (primary key) */
	Timestamp time.Time `json:"timestamp" gorm:"primaryKey"`

	/** Interfaces contains metrics for each network interface at this timestamp */
	Interfaces []NetworkInterface `json:"interfaces" gorm:"serializer:json"`
}

/**
 * GetTimestamp returns the timestamp when this network metric was recorded.
 * @return time.Time The recording timestamp
 */
func (h HistoricalNetworkMetric) GetTimestamp() time.Time { return h.Timestamp }

/**
 * GetMetricType returns the metric type identifier for network metrics.
 * @return string Always returns "network"
 */
func (h HistoricalNetworkMetric) GetMetricType() string { return "network" }

/**
 * TableName returns the database table name for GORM operations.
 * @return string The table name "network_metrics"
 */
func (HistoricalNetworkMetric) TableName() string { return "network_metrics" }
