package network

import (
	"sync"
	"time"
)

type NetworkSpeed struct {
	SpeedMbps     float64
	Throughput    float64
	SpeedKbpsSent float64
	SpeedKbpsRecv float64
}

// speedMinInterval is the shortest window over which a network rate is
// meaningful. Speed = byte delta / time delta, so two batches a few
// milliseconds apart (the per-cycle DB save and the SSE/replication snapshot)
// would divide by ~0 and produce a garbage spike. A batch that starts within
// this window of the previous one reuses the last computed speeds and leaves
// the baseline untouched.
const speedMinInterval = 3 * time.Second

type NetworkSpeedCalculator struct {
	mu               sync.RWMutex
	lastTimestamp    time.Time
	interfaceData    map[string]NetworkInterfaceData
	pendingTimestamp time.Time
	lastSpeed        map[string]NetworkSpeed
	reuse            bool
}

type NetworkInterfaceData struct {
	Timestamp   time.Time
	BytesSent   uint64
	BytesRecv   uint64
	PacketsSent uint64
	PacketsRecv uint64
}

func NewNetworkSpeedCalculator() *NetworkSpeedCalculator {
	return &NetworkSpeedCalculator{
		interfaceData: make(map[string]NetworkInterfaceData),
		lastSpeed:     make(map[string]NetworkSpeed),
	}
}

func (c *NetworkSpeedCalculator) BeginCalculationBatch() {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	// A batch within speedMinInterval of the last one (e.g. the per-cycle save
	// and the SSE/replication snapshot) would compute a meaningless sub-second
	// rate and reset the baseline — reuse the previous speeds instead.
	c.reuse = !c.lastTimestamp.IsZero() && now.Sub(c.lastTimestamp) < speedMinInterval
	if !c.reuse {
		c.pendingTimestamp = now
	}
}

func (c *NetworkSpeedCalculator) EndCalculationBatch() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.reuse {
		return
	}
	c.lastTimestamp = c.pendingTimestamp
}

func (c *NetworkSpeedCalculator) CalculateSpeed(
	name string,
	currentBytesSent, currentBytesRecv, currentPacketsSent, currentPacketsRecv uint64,
) NetworkSpeed {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Sub-interval re-collection: return the last meaningful speed for this
	// interface instead of recomputing over a near-zero time delta.
	if c.reuse {
		return c.lastSpeed[name]
	}

	currentTime := c.pendingTimestamp
	if currentTime.IsZero() {
		currentTime = time.Now()
	}

	currentData := NetworkInterfaceData{
		Timestamp:   currentTime,
		BytesSent:   currentBytesSent,
		BytesRecv:   currentBytesRecv,
		PacketsSent: currentPacketsSent,
		PacketsRecv: currentPacketsRecv,
	}

	throughput := float64(currentBytesSent + currentBytesRecv)

	if c.lastTimestamp.IsZero() || currentTime.Sub(c.lastTimestamp) > time.Minute {
		c.interfaceData[name] = currentData
		return NetworkSpeed{SpeedMbps: 0, Throughput: throughput, SpeedKbpsSent: 0, SpeedKbpsRecv: 0}
	}

	prev, exists := c.interfaceData[name]
	if !exists {
		c.interfaceData[name] = currentData
		return NetworkSpeed{SpeedMbps: 0, Throughput: throughput, SpeedKbpsSent: 0, SpeedKbpsRecv: 0}
	}

	timeDiff := currentTime.Sub(c.lastTimestamp).Seconds()
	if timeDiff <= 0 {
		return NetworkSpeed{SpeedMbps: 0, Throughput: throughput, SpeedKbpsSent: 0, SpeedKbpsRecv: 0}
	}

	sentBytesPerSecond := float64(currentBytesSent-prev.BytesSent) / timeDiff
	recvBytesPerSecond := float64(currentBytesRecv-prev.BytesRecv) / timeDiff

	speedKbpsSent := (sentBytesPerSecond * 8) / 1000
	speedKbpsRecv := (recvBytesPerSecond * 8) / 1000

	totalBytesPerSecond := sentBytesPerSecond + recvBytesPerSecond
	speedMbps := (totalBytesPerSecond * 8) / 1000000

	c.interfaceData[name] = currentData

	speed := NetworkSpeed{
		SpeedMbps:     speedMbps,
		Throughput:    throughput,
		SpeedKbpsSent: speedKbpsSent,
		SpeedKbpsRecv: speedKbpsRecv,
	}
	c.lastSpeed[name] = speed
	return speed
}
