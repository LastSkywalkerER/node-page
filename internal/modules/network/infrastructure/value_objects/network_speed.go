/**
 * Package value_objects provides domain value objects for complex calculations and business rules.
 * This package contains immutable objects that encapsulate business logic and calculations,
 * particularly for network speed computations and other derived metrics.
 */
package value_objects

import (
	"sync"
	"time"
)

/**
 * NetworkSpeed represents calculated network interface speed and throughput metrics.
 * This value object encapsulates the results of network speed calculations,
 * providing both bandwidth speed and total throughput measurements.
 */
type NetworkSpeed struct {
	/** SpeedMbps represents the calculated network speed in megabits per second */
	SpeedMbps float64

	/** Throughput represents the total data transfer rate in bytes */
	Throughput float64

	/** SpeedKbpsSent represents the upload speed in kilobits per second */
	SpeedKbpsSent float64

	/** SpeedKbpsRecv represents the download speed in kilobits per second */
	SpeedKbpsRecv float64
}

/**
 * NetworkSpeedCalculator calculates network interface speeds based on historical data.
 * This calculator maintains state for multiple network interfaces and computes
 * speed metrics by comparing current measurements with previous snapshots.
 */
type NetworkSpeedCalculator struct {
	/** mu protects concurrent access to calculator internal state */
	mu sync.RWMutex

	/** lastTimestamp tracks the last time speed calculations were performed */
	lastTimestamp time.Time

	/** interfaceData stores historical data for each network interface */
	interfaceData map[string]NetworkInterfaceData

	/** pendingTimestamp stores the timestamp for the current calculation batch */
	pendingTimestamp time.Time
}

/**
 * NetworkInterfaceData stores network interface metrics for speed calculation.
 * This structure holds a snapshot of network interface statistics at a specific point in time,
 * used as reference data for calculating speed changes over time.
 */
type NetworkInterfaceData struct {
	/** Timestamp indicates when these interface metrics were recorded */
	Timestamp time.Time

	/** BytesSent shows total bytes transmitted since system start at this timestamp */
	BytesSent uint64

	/** BytesRecv shows total bytes received since system start at this timestamp */
	BytesRecv uint64

	/** PacketsSent shows total packets transmitted since system start at this timestamp */
	PacketsSent uint64

	/** PacketsRecv shows total packets received since system start at this timestamp */
	PacketsRecv uint64
}

/**
 * NewNetworkSpeedCalculator creates a new instance of the network speed calculator.
 * This constructor initializes the calculator with an empty interface data map
 * and prepares it for tracking network interface metrics.
 *
 * @return *NetworkSpeedCalculator Returns the initialized network speed calculator
 */
func NewNetworkSpeedCalculator() *NetworkSpeedCalculator {
	return &NetworkSpeedCalculator{
		interfaceData: make(map[string]NetworkInterfaceData),
	}
}

/**
 * BeginCalculationBatch starts a new batch of speed calculations with a consistent timestamp.
 * This method should be called before calculating speeds for multiple interfaces
 * to ensure all calculations use the same time reference.
 */
func (c *NetworkSpeedCalculator) BeginCalculationBatch() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pendingTimestamp = time.Now()
}

/**
 * EndCalculationBatch completes a batch of speed calculations and updates the last timestamp.
 * This method should be called after calculating speeds for all interfaces in a batch.
 */
func (c *NetworkSpeedCalculator) EndCalculationBatch() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lastTimestamp = c.pendingTimestamp
}

/**
 * CalculateSpeed calculates the network speed for a specific interface based on current metrics.
 * This method compares current network interface statistics with previously stored data
 * to compute bandwidth speed in Mbps and total throughput. The calculation requires
 * at least two data points to compute meaningful speed metrics.
 *
 * @param name The network interface name (e.g., "eth0", "wlan0")
 * @param currentBytesSent Current total bytes sent since system start
 * @param currentBytesRecv Current total bytes received since system start
 * @param currentPacketsSent Current total packets sent since system start
 * @param currentPacketsRecv Current total packets received since system start
 * @return NetworkSpeed The calculated speed and throughput metrics
 */
func (c *NetworkSpeedCalculator) CalculateSpeed(
	name string,
	currentBytesSent, currentBytesRecv, currentPacketsSent, currentPacketsRecv uint64,
) NetworkSpeed {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Use pending timestamp if set, otherwise use current time
	currentTime := c.pendingTimestamp
	if currentTime.IsZero() {
		currentTime = time.Now()
	}

	// Create a snapshot of current interface data
	currentData := NetworkInterfaceData{
		Timestamp:   currentTime,
		BytesSent:   currentBytesSent,
		BytesRecv:   currentBytesRecv,
		PacketsSent: currentPacketsSent,
		PacketsRecv: currentPacketsRecv,
	}

	// Calculate total throughput (sum of all bytes transferred)
	throughput := float64(currentBytesSent + currentBytesRecv)

	// Initialize speed calculation on first run or after long periods of inactivity
	// This prevents inaccurate calculations from stale data
	if c.lastTimestamp.IsZero() || currentTime.Sub(c.lastTimestamp) > time.Minute {
		c.interfaceData[name] = currentData
		return NetworkSpeed{SpeedMbps: 0, Throughput: throughput, SpeedKbpsSent: 0, SpeedKbpsRecv: 0}
	}

	// Retrieve previous data for this interface
	prev, exists := c.interfaceData[name]
	if !exists {
		// First measurement for this interface - store and return zero speed
		c.interfaceData[name] = currentData
		return NetworkSpeed{SpeedMbps: 0, Throughput: throughput, SpeedKbpsSent: 0, SpeedKbpsRecv: 0}
	}

	// Calculate time difference between measurements in seconds
	timeDiff := currentTime.Sub(c.lastTimestamp).Seconds()
	if timeDiff <= 0 {
		// Prevent division by zero or negative time differences
		return NetworkSpeed{SpeedMbps: 0, Throughput: throughput, SpeedKbpsSent: 0, SpeedKbpsRecv: 0}
	}

	// Calculate bytes per second for sent and received separately
	sentBytesPerSecond := float64(currentBytesSent-prev.BytesSent) / timeDiff
	recvBytesPerSecond := float64(currentBytesRecv-prev.BytesRecv) / timeDiff

	// Convert bytes per second to kilobits per second
	// Formula: (bytes/second * 8 bits/byte) / 1,000 bits/kilobit
	speedKbpsSent := (sentBytesPerSecond * 8) / 1000
	speedKbpsRecv := (recvBytesPerSecond * 8) / 1000

	// Calculate total bytes per second for Mbps calculation
	totalBytesPerSecond := sentBytesPerSecond + recvBytesPerSecond

	// Convert bytes per second to megabits per second
	// Formula: (bytes/second * 8 bits/byte) / 1,000,000 bits/megabit
	speedMbps := (totalBytesPerSecond * 8) / 1000000

	// Update stored data for next calculation
	c.interfaceData[name] = currentData

	return NetworkSpeed{
		SpeedMbps:     speedMbps,
		Throughput:    throughput,
		SpeedKbpsSent: speedKbpsSent,
		SpeedKbpsRecv: speedKbpsRecv,
	}
}
