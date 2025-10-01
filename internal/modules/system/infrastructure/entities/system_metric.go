package entities

import (
	"time"

	cpumetric "system-stats/internal/modules/cpu/infrastructure/entities"
	diskmetric "system-stats/internal/modules/disk/infrastructure/entities"
	dockermetric "system-stats/internal/modules/docker/infrastructure/entities"
	memorymetric "system-stats/internal/modules/memory/infrastructure/entities"
	networkmetric "system-stats/internal/modules/network/infrastructure/entities"
)

/**
 * SystemMetric aggregates all system performance metrics into a single structure.
 * This is the main entity that represents a complete snapshot of system state
 * at a specific point in time, including CPU, memory, disk, network, and Docker metrics.
 */
type SystemMetric struct {
	/** Timestamp indicates when these metrics were collected */
	Timestamp time.Time `json:"timestamp"`

	/** CPU contains central processing unit performance metrics */
	CPU cpumetric.CPUMetric `json:"cpu"`

	/** Memory contains RAM and swap memory usage metrics */
	Memory memorymetric.MemoryMetric `json:"memory"`

	/** Disk contains storage utilization and I/O metrics */
	Disk diskmetric.DiskMetric `json:"disk"`

	/** Network contains network interface traffic and performance metrics */
	Network networkmetric.NetworkMetric `json:"network"`

	/** Docker contains Docker container metrics and status information */
	Docker dockermetric.DockerMetric `json:"docker"`
}

/**
 * GetTimestamp returns the timestamp when these system metrics were collected.
 * @return time.Time The timestamp of metric collection
 */
func (s SystemMetric) GetTimestamp() time.Time { return s.Timestamp }

/**
 * GetType returns the metric type identifier for system metrics.
 * @return string Always returns "system"
 */
func (s SystemMetric) GetType() string { return "system" }
