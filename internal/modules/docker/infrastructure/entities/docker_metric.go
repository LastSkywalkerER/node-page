package entities

import (
	"encoding/json"
	"time"
)

/**
 * DockerStack represents a group of Docker containers that belong to the same docker-compose project.
 * This structure groups containers by their compose project name and provides aggregate statistics.
 */
type DockerStack struct {
	/** Name is the name of the docker-compose project/stack */
	Name string `json:"name"`

	/** Containers contains all containers in this stack */
	Containers []DockerContainer `json:"containers"`

	/** TotalContainers shows the total number of containers in this stack (including stopped) */
	TotalContainers int `json:"total_containers"`

	/** RunningContainers shows the number of currently running containers in this stack */
	RunningContainers int `json:"running_containers"`
}

/**
 * DockerMetric represents Docker daemon and container metrics.
 * This structure provides information about Docker availability, container counts,
 * and grouped container statistics by docker-compose stacks.
 */
type DockerMetric struct {
	/** Stacks contains grouped information about Docker containers organized by compose projects */
	Stacks []DockerStack `json:"stacks"`

	/** TotalContainers shows the total number of containers (including stopped) */
	TotalContainers int `json:"total_containers"`

	/** RunningContainers shows the number of currently running containers */
	RunningContainers int `json:"running_containers"`

	/** DockerAvailable indicates whether the Docker daemon is accessible */
	DockerAvailable bool `json:"docker_available"`

	/** Error contains any error message if Docker metrics collection failed */
	Error string `json:"error,omitempty"`
}

/**
 * GetTimestamp returns the current time for Docker metrics.
 * @return time.Time The current timestamp
 */
func (d DockerMetric) GetTimestamp() time.Time { return time.Now() }

/**
 * GetType returns the metric type identifier for Docker metrics.
 * @return string Always returns "docker"
 */
func (d DockerMetric) GetType() string { return "docker" }

/**
 * DockerContainer represents a Docker container with its metadata and runtime statistics.
 * This structure contains information about container identity, configuration, and performance metrics.
 */
type DockerContainer struct {
	/** ID is the unique Docker container identifier */
	ID string `json:"id"`

	/** Name is the human-readable container name */
	Name string `json:"name"`

	/** Image shows the Docker image used to create this container */
	Image string `json:"image"`

	/** State indicates the current container state (running, stopped, paused, etc.) */
	State string `json:"state"`

	/** Status provides a human-readable status description */
	Status string `json:"status"`

	/** Ports contains port mapping information for the container */
	Ports []DockerPort `json:"ports"`

	/** Stats contains real-time performance statistics for the container */
	Stats DockerStats `json:"stats"`

	/** Created shows when the container was created (ISO 8601 timestamp) */
	Created string `json:"created"`

	/** FinishedAt shows when the container finished (ISO 8601 timestamp, for exited containers) */
	FinishedAt string `json:"finished_at,omitempty"`
}

/**
 * DockerContainerEntity represents a Docker container stored in the database.
 * This entity is used for database storage with foreign key relationship to DockerMetric.
 */
type DockerContainerEntity struct {
	/** ID is the unique Docker container identifier (primary key) */
	ID string `gorm:"primaryKey"`

	/** MetricTimestamp references the timestamp of the parent DockerMetric */
	MetricTimestamp time.Time `gorm:"primaryKey;column:metric_timestamp"`

	/** Name is the human-readable container name */
	Name string `gorm:"column:name"`

	/** Image shows the Docker image used to create this container */
	Image string `gorm:"column:image"`

	/** State indicates the current container state (running, stopped, paused, etc.) */
	State string `gorm:"column:state"`

	/** Status provides a human-readable status description */
	Status string `gorm:"column:status"`

	/** Ports contains port mapping information serialized as JSON */
	Ports string `gorm:"column:ports;type:text"`

	/** CPUPercent shows the container's CPU utilization as a percentage */
	CPUPercent float64 `gorm:"column:cpu_percent"`

	/** CPULimit shows the CPU limit set for the container */
	CPULimit float64 `gorm:"column:cpu_limit"`

	/** CPUPercentOfLimit shows CPU utilization as a percentage of the container's CPU limit */
	CPUPercentOfLimit float64 `gorm:"column:cpu_percent_of_limit"`

	/** MemoryUsage shows current memory usage in bytes */
	MemoryUsage uint64 `gorm:"column:memory_usage"`

	/** MemoryLimit shows the memory limit set for the container in bytes */
	MemoryLimit uint64 `gorm:"column:memory_limit"`

	/** MemoryPercent shows memory utilization as a percentage */
	MemoryPercent float64 `gorm:"column:memory_percent"`

	/** NetworkRx shows total bytes received over the network */
	NetworkRx uint64 `gorm:"column:network_rx"`

	/** NetworkTx shows total bytes transmitted over the network */
	NetworkTx uint64 `gorm:"column:network_tx"`

	/** BlockRead shows total bytes read from block devices */
	BlockRead uint64 `gorm:"column:block_read"`

	/** BlockWrite shows total bytes written to block devices */
	BlockWrite uint64 `gorm:"column:block_write"`

	/** Created shows when the container was created (ISO 8601 timestamp) */
	Created string `gorm:"column:created"`

	/** FinishedAt shows when the container finished (ISO 8601 timestamp, for exited containers) */
	FinishedAt string `gorm:"column:finished_at"`
}

/**
 * DockerPort represents a port mapping for a Docker container.
 * This structure describes how container ports are mapped to host ports.
 */
type DockerPort struct {
	/** PrivatePort is the port number inside the container */
	PrivatePort int `json:"private_port"`

	/** PublicPort is the port number on the host (optional, for exposed ports) */
	PublicPort int `json:"public_port,omitempty"`

	/** Type indicates the protocol type (tcp, udp) */
	Type string `json:"type"`

	/** IP specifies the IP address for port binding (optional) */
	IP string `json:"ip,omitempty"`
}

/**
 * DockerStats represents real-time performance statistics for a Docker container.
 * This structure contains CPU, memory, network, and I/O usage information.
 */
type DockerStats struct {
	/** CPUPercent shows the container's CPU utilization relative to system as a percentage */
	CPUPercent float64 `json:"cpu_percent"`

	/** CPULimit shows the CPU limit set for the container (in CPU cores or equivalent) */
	CPULimit float64 `json:"cpu_limit"`

	/** CPUPercentOfLimit shows CPU utilization as a percentage of the container's CPU limit */
	CPUPercentOfLimit float64 `json:"cpu_percent_of_limit"`

	/** MemoryUsage shows current memory usage in bytes */
	MemoryUsage uint64 `json:"memory_usage"`

	/** MemoryLimit shows the memory limit set for the container in bytes */
	MemoryLimit uint64 `json:"memory_limit"`

	/** MemoryPercent shows memory utilization as a percentage */
	MemoryPercent float64 `json:"memory_percent"`

	/** NetworkRx shows total bytes received over the network */
	NetworkRx uint64 `json:"network_rx"`

	/** NetworkTx shows total bytes transmitted over the network */
	NetworkTx uint64 `json:"network_tx"`

	/** BlockRead shows total bytes read from block devices */
	BlockRead uint64 `json:"block_read"`

	/** BlockWrite shows total bytes written to block devices */
	BlockWrite uint64 `json:"block_write"`
}

/**
 * ToDockerContainer converts a DockerContainerEntity to a DockerContainer.
 * This method is used for API responses and data transformation.
 *
 * @param metricTimestamp The timestamp of the parent metric
 * @return DockerContainer The converted container
 */
func (e DockerContainerEntity) ToDockerContainer() (DockerContainer, error) {
	var ports []DockerPort
	if err := json.Unmarshal([]byte(e.Ports), &ports); err != nil {
		ports = []DockerPort{} // Default to empty slice on error
	}

	return DockerContainer{
		ID:     e.ID,
		Name:   e.Name,
		Image:  e.Image,
		State:  e.State,
		Status: e.Status,
		Ports:  ports,
		Stats: DockerStats{
			CPUPercent:        e.CPUPercent,
			CPULimit:          e.CPULimit,
			CPUPercentOfLimit: e.CPUPercentOfLimit,
			MemoryUsage:       e.MemoryUsage,
			MemoryLimit:       e.MemoryLimit,
			MemoryPercent:     e.MemoryPercent,
			NetworkRx:         e.NetworkRx,
			NetworkTx:         e.NetworkTx,
			BlockRead:         e.BlockRead,
			BlockWrite:        e.BlockWrite,
		},
		Created:    e.Created,
		FinishedAt: e.FinishedAt,
	}, nil
}

/**
 * ToDockerContainerEntity converts a DockerContainer to a DockerContainerEntity.
 * This method is used for database storage preparation.
 *
 * @param metricTimestamp The timestamp of the parent metric
 * @return DockerContainerEntity The converted entity
 */
func (c DockerContainer) ToDockerContainerEntity(metricTimestamp time.Time) (DockerContainerEntity, error) {
	portsJSON, err := json.Marshal(c.Ports)
	if err != nil {
		portsJSON = []byte("[]") // Default to empty array on error
	}

	return DockerContainerEntity{
		ID:                c.ID,
		MetricTimestamp:   metricTimestamp,
		Name:              c.Name,
		Image:             c.Image,
		State:             c.State,
		Status:            c.Status,
		Ports:             string(portsJSON),
		CPUPercent:        c.Stats.CPUPercent,
		CPULimit:          c.Stats.CPULimit,
		CPUPercentOfLimit: c.Stats.CPUPercentOfLimit,
		MemoryUsage:       c.Stats.MemoryUsage,
		MemoryLimit:       c.Stats.MemoryLimit,
		MemoryPercent:     c.Stats.MemoryPercent,
		NetworkRx:         c.Stats.NetworkRx,
		NetworkTx:         c.Stats.NetworkTx,
		BlockRead:         c.Stats.BlockRead,
		BlockWrite:        c.Stats.BlockWrite,
		Created:           c.Created,
		FinishedAt:        c.FinishedAt,
	}, nil
}
