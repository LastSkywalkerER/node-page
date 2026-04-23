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

type NetworkSpeedCalculator struct {
	mu               sync.RWMutex
	lastTimestamp    time.Time
	interfaceData    map[string]NetworkInterfaceData
	pendingTimestamp time.Time
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
	}
}

func (c *NetworkSpeedCalculator) BeginCalculationBatch() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pendingTimestamp = time.Now()
}

func (c *NetworkSpeedCalculator) EndCalculationBatch() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lastTimestamp = c.pendingTimestamp
}

func (c *NetworkSpeedCalculator) CalculateSpeed(
	name string,
	currentBytesSent, currentBytesRecv, currentPacketsSent, currentPacketsRecv uint64,
) NetworkSpeed {
	c.mu.Lock()
	defer c.mu.Unlock()

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

	return NetworkSpeed{
		SpeedMbps:     speedMbps,
		Throughput:    throughput,
		SpeedKbpsSent: speedKbpsSent,
		SpeedKbpsRecv: speedKbpsRecv,
	}
}
