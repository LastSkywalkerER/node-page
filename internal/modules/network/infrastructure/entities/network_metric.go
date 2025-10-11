package entities

import (
	"time"
)

/**
 * NetworkMetric represents network interface performance metrics.
 * This structure aggregates information about all network interfaces
 * including traffic statistics and packet counts.
 */
type NetworkMetric struct {
	/** Interfaces contains metrics for each network interface */
	Interfaces []NetworkInterface `json:"interfaces"`
}

/**
 * GetTimestamp returns the current time for network metrics.
 * @return time.Time The current timestamp
 */
func (n NetworkMetric) GetTimestamp() time.Time { return time.Now() }

/**
 * GetType returns the metric type identifier for network metrics.
 * @return string Always returns "network"
 */
func (n NetworkMetric) GetType() string { return "network" }

/**
 * NetworkInterface represents a single network interface's traffic statistics.
 * This structure contains byte and packet counters for network monitoring.
 */
type NetworkInterface struct {
	/** Name is the network interface name (e.g., "eth0", "wlan0") */
	Name string `json:"name"`

	/** IPs lists all IP addresses assigned to this interface (IPv4/IPv6) */
	IPs []string `json:"ips"`

	/** Mac is the hardware (MAC) address of the interface */
	Mac string `json:"mac"`

	/** BytesSent shows total bytes transmitted since system start */
	BytesSent uint64 `json:"bytes_sent"`

	/** BytesRecv shows total bytes received since system start */
	BytesRecv uint64 `json:"bytes_recv"`

	/** PacketsSent shows total packets transmitted since system start */
	PacketsSent uint64 `json:"packets_sent"`

	/** PacketsRecv shows total packets received since system start */
	PacketsRecv uint64 `json:"packets_recv"`

	/** SpeedKbpsSent shows current upload speed in kilobits per second */
	SpeedKbpsSent float64 `json:"speed_kbps_sent"`

	/** SpeedKbpsRecv shows current download speed in kilobits per second */
	SpeedKbpsRecv float64 `json:"speed_kbps_recv"`

	/** IsPrimary indicates whether this interface is the system's primary outbound interface */
	IsPrimary bool `json:"is_primary"`

	// Error/drop counters
	Errin   uint64 `json:"errin"`
	Errout  uint64 `json:"errout"`
	Dropin  uint64 `json:"dropin"`
	Dropout uint64 `json:"dropout"`
}

/**
 * NetworkSpeed represents calculated network interface speeds.
 * This structure contains bandwidth and throughput calculations for network monitoring.
 */
type NetworkSpeed struct {
	/** SpeedMbps shows the current network speed in megabits per second */
	SpeedMbps float64

	/** Throughput shows the current data transfer rate */
	Throughput float64

	/** SpeedKbpsSent shows the upload speed in kilobits per second */
	SpeedKbpsSent float64

	/** SpeedKbpsRecv shows the download speed in kilobits per second */
	SpeedKbpsRecv float64
}
