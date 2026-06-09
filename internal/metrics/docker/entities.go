package docker

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

// DockerStack represents a group of Docker containers that belong to the same docker-compose project.
// This structure groups containers by their compose project name and provides aggregate statistics.
type DockerStack struct {
	// Name is the name of the docker-compose project/stack
	Name string `json:"name"`

	// Containers contains all containers in this stack
	Containers []DockerContainer `json:"containers"`

	// TotalContainers shows the total number of containers in this stack (including stopped)
	TotalContainers int `json:"total_containers"`

	// RunningContainers shows the number of currently running containers in this stack
	RunningContainers int `json:"running_containers"`
}

// DockerMetric represents Docker daemon and container metrics.
// This structure provides information about Docker availability, container counts,
// and grouped container statistics by docker-compose stacks.
type DockerMetric struct {
	// Stacks contains grouped information about Docker containers organized by compose projects
	Stacks []DockerStack `json:"stacks"`

	// TotalContainers shows the total number of containers (including stopped)
	TotalContainers int `json:"total_containers"`

	// RunningContainers shows the number of currently running containers
	RunningContainers int `json:"running_containers"`

	// DockerAvailable indicates whether the Docker daemon is accessible
	DockerAvailable bool `json:"docker_available"`

	// Error contains any error message if Docker metrics collection failed
	Error string `json:"error,omitempty"`
}

// GetTimestamp returns the current time for Docker metrics.
func (d DockerMetric) GetTimestamp() time.Time { return time.Now() }

// GetType returns the metric type identifier for Docker metrics.
func (d DockerMetric) GetType() string { return "docker" }

// DockerContainer represents a Docker container with its metadata and runtime statistics.
// This structure contains information about container identity, configuration, and performance metrics.
type DockerContainer struct {
	// ID is the unique Docker container identifier
	ID string `json:"id"`

	// Name is the human-readable container name
	Name string `json:"name"`

	// Image shows the Docker image used to create this container. For swarm /
	// dokploy deployments whose container-list image is a bare sha256: digest,
	// this is resolved to a human-readable repo:tag when possible.
	Image string `json:"image"`

	// ConfigImage is the container's configured image reference (Config.Image),
	// a more reliable repo:tag source than Image for swarm. In-memory only: it
	// seeds the registry update check and is not persisted or sent to the client.
	ConfigImage string `json:"-" gorm:"-"`

	// State indicates the current container state (running, stopped, paused, etc.)
	State string `json:"state"`

	// Status provides a human-readable status description
	Status string `json:"status"`

	// Ports contains port mapping information for the container
	Ports []DockerPort `json:"ports"`

	// Stats contains real-time performance statistics for the container
	Stats DockerStats `json:"stats"`

	// Created shows when the container was created (ISO 8601 timestamp)
	Created string `json:"created"`

	// FinishedAt shows when the container finished (ISO 8601 timestamp, for exited containers)
	FinishedAt string `json:"finished_at,omitempty"`

	// Project is the resolved application grouping key (compose project / swarm
	// namespace / standalone container name).
	Project string `json:"project,omitempty"`

	// Service is the service name within the application.
	Service string `json:"service,omitempty"`

	// Labels holds the container's Docker labels (icon + public-link detection,
	// compose composition view).
	Labels map[string]string `json:"labels,omitempty"`

	// ComposeConfigFiles is the com.docker.compose.project.config_files label value.
	ComposeConfigFiles string `json:"compose_config_files,omitempty"`

	// ComposeWorkingDir is the com.docker.compose.project.working_dir label value.
	ComposeWorkingDir string `json:"compose_working_dir,omitempty"`

	// SizeRw is the size of the container's writable layer in bytes (data written
	// since creation). Requires size collection on the container list.
	SizeRw int64 `json:"size_rw,omitempty"`

	// SizeRootFs is the total size of the container filesystem in bytes
	// (image layers + writable layer). Image layers are shared across containers
	// of the same image, so summing this across an app over-counts shared layers.
	SizeRootFs int64 `json:"size_root_fs,omitempty"`

	// Mounts are the container's volume / bind mounts (with host paths).
	Mounts []DockerMount `json:"mounts,omitempty"`

	// ImageID is the content-addressable image ID (sha256:...) the container runs.
	ImageID string `json:"image_id,omitempty"`

	// UpdateAvailable is true when the registry has a newer image for this tag
	// than the one currently running (digest mismatch).
	UpdateAvailable bool `json:"update_available,omitempty"`

	// UpdateChecked indicates an update check has completed for this image
	// (false = not yet checked or the registry was unreachable).
	UpdateChecked bool `json:"update_checked,omitempty"`

	// LocalDigest is the digest of the running image (repo@sha256:...).
	LocalDigest string `json:"local_digest,omitempty"`

	// RemoteDigest is the digest the registry tag currently points to.
	RemoteDigest string `json:"remote_digest,omitempty"`

	// ImageVersion is the running image's resolved human-readable version.
	ImageVersion string `json:"image_version,omitempty"`

	// RemoteVersion is the version of the image the registry tag now points to
	// (resolved from the remote image config). Empty when not resolvable.
	RemoteVersion string `json:"remote_version,omitempty"`

	// NewerVersion is the newest registry tag with the SAME major as the running
	// image (e.g. running 1.2.1 → 1.3.2 available). Empty when none / not pinned.
	NewerVersion string `json:"newer_version,omitempty"`

	// NewerMajorVersion is the newest registry tag with a HIGHER major than the
	// running image (e.g. running 1.2.1 → 2.0.0 available; possibly breaking).
	// Empty when none.
	NewerMajorVersion string `json:"newer_major_version,omitempty"`
}

// DockerMount describes a single volume or bind mount of a container.
type DockerMount struct {
	// Type is the mount type: "volume", "bind", or "tmpfs".
	Type string `json:"type"`
	// Name is the volume name (named volumes only).
	Name string `json:"name,omitempty"`
	// Source is the host path (or volume data dir) backing the mount.
	Source string `json:"source"`
	// Destination is the mount path inside the container.
	Destination string `json:"destination"`
	// RW indicates whether the mount is read-write (false = read-only).
	RW bool `json:"rw"`
}

// DockerContainerEntity represents a Docker container stored in the database.
// This entity is used for database storage with foreign key relationship to DockerMetric.
type DockerContainerEntity struct {
	// ID is the unique Docker container identifier (primary key)
	ID string `gorm:"primaryKey"`

	// MetricTimestamp references the timestamp of the parent DockerMetric
	MetricTimestamp time.Time `gorm:"primaryKey;column:metric_timestamp"`

	// Name is the human-readable container name
	Name string `gorm:"column:name"`

	// Image shows the Docker image used to create this container
	Image string `gorm:"column:image"`

	// State indicates the current container state (running, stopped, paused, etc.)
	State string `gorm:"column:state"`

	// Status provides a human-readable status description
	Status string `gorm:"column:status"`

	// Ports contains port mapping information serialized as JSON
	Ports string `gorm:"column:ports;type:text"`

	// CPUPercent shows the container's CPU utilization as a percentage
	CPUPercent float64 `gorm:"column:cpu_percent"`

	// CPULimit shows the CPU limit set for the container
	CPULimit float64 `gorm:"column:cpu_limit"`

	// CPUPercentOfLimit shows CPU utilization as a percentage of the container's CPU limit
	CPUPercentOfLimit float64 `gorm:"column:cpu_percent_of_limit"`

	// MemoryUsage shows current memory usage in bytes
	MemoryUsage uint64 `gorm:"column:memory_usage"`

	// MemoryLimit shows the memory limit set for the container in bytes
	MemoryLimit uint64 `gorm:"column:memory_limit"`

	// MemoryPercent shows memory utilization as a percentage
	MemoryPercent float64 `gorm:"column:memory_percent"`

	// NetworkRx shows total bytes received over the network
	NetworkRx uint64 `gorm:"column:network_rx"`

	// NetworkTx shows total bytes transmitted over the network
	NetworkTx uint64 `gorm:"column:network_tx"`

	// BlockRead shows total bytes read from block devices
	BlockRead uint64 `gorm:"column:block_read"`

	// BlockWrite shows total bytes written to block devices
	BlockWrite uint64 `gorm:"column:block_write"`

	// Created shows when the container was created (ISO 8601 timestamp)
	Created string `gorm:"column:created"`

	// FinishedAt shows when the container finished (ISO 8601 timestamp, for exited containers)
	FinishedAt string `gorm:"column:finished_at"`

	// Project is the resolved application grouping key (indexed).
	Project string `gorm:"column:project;index"`

	// Service is the service name within the application.
	Service string `gorm:"column:service"`

	// Labels holds the container's Docker labels serialized as a JSON object.
	Labels string `gorm:"column:labels;type:text"`

	// ComposeConfigFiles is the com.docker.compose.project.config_files label value.
	ComposeConfigFiles string `gorm:"column:compose_config_files;type:text"`

	// ComposeWorkingDir is the com.docker.compose.project.working_dir label value.
	ComposeWorkingDir string `gorm:"column:compose_working_dir"`

	// SizeRw / SizeRootFs are the writable-layer and total filesystem sizes (bytes).
	SizeRw     int64 `gorm:"column:size_rw"`
	SizeRootFs int64 `gorm:"column:size_root_fs"`

	// Mounts holds the container's volume/bind mounts serialized as a JSON array.
	Mounts string `gorm:"column:mounts;type:text"`

	// ImageID / UpdateAvailable / UpdateChecked back the image update-check feature.
	ImageID         string `gorm:"column:image_id"`
	UpdateAvailable bool   `gorm:"column:update_available"`
	UpdateChecked   bool   `gorm:"column:update_checked"`
	LocalDigest     string `gorm:"column:local_digest"`
	RemoteDigest    string `gorm:"column:remote_digest"`
	ImageVersion    string `gorm:"column:image_version"`
	RemoteVersion   string `gorm:"column:remote_version"`
	// NewerVersion / NewerMajorVersion back the newer-version-tag detection.
	NewerVersion      string `gorm:"column:newer_version"`
	NewerMajorVersion string `gorm:"column:newer_major_version"`
}

// DockerPort represents a port mapping for a Docker container.
// This structure describes how container ports are mapped to host ports.
type DockerPort struct {
	// PrivatePort is the port number inside the container
	PrivatePort int `json:"private_port"`

	// PublicPort is the port number on the host (optional, for exposed ports)
	PublicPort int `json:"public_port,omitempty"`

	// Type indicates the protocol type (tcp, udp)
	Type string `json:"type"`

	// IP specifies the IP address for port binding (optional)
	IP string `json:"ip,omitempty"`

	// PublicURL is the external URL a reverse proxy (Traefik file provider /
	// labels) routes to this port, when one was detected. Present even for
	// ports the container does not publish to the host.
	PublicURL string `json:"public_url,omitempty"`
}

// DockerStats represents real-time performance statistics for a Docker container.
// This structure contains CPU, memory, network, and I/O usage information.
type DockerStats struct {
	// CPUPercent shows the container's CPU utilization relative to system as a percentage
	CPUPercent float64 `json:"cpu_percent"`

	// CPULimit shows the CPU limit set for the container (in CPU cores or equivalent)
	CPULimit float64 `json:"cpu_limit"`

	// CPUPercentOfLimit shows CPU utilization as a percentage of the container's CPU limit
	CPUPercentOfLimit float64 `json:"cpu_percent_of_limit"`

	// MemoryUsage shows current memory usage in bytes
	MemoryUsage uint64 `json:"memory_usage"`

	// MemoryLimit shows the memory limit set for the container in bytes
	MemoryLimit uint64 `json:"memory_limit"`

	// MemoryPercent shows memory utilization as a percentage
	MemoryPercent float64 `json:"memory_percent"`

	// NetworkRx shows total bytes received over the network
	NetworkRx uint64 `json:"network_rx"`

	// NetworkTx shows total bytes transmitted over the network
	NetworkTx uint64 `json:"network_tx"`

	// BlockRead shows total bytes read from block devices
	BlockRead uint64 `json:"block_read"`

	// BlockWrite shows total bytes written to block devices
	BlockWrite uint64 `json:"block_write"`
}

// ToDockerContainer converts a DockerContainerEntity to a DockerContainer.
// This method is used for API responses and data transformation.
func (e DockerContainerEntity) ToDockerContainer() (DockerContainer, error) {
	var ports []DockerPort
	if err := json.Unmarshal([]byte(e.Ports), &ports); err != nil {
		ports = []DockerPort{} // Default to empty slice on error
	}

	var labels map[string]string
	if e.Labels != "" {
		if err := json.Unmarshal([]byte(e.Labels), &labels); err != nil {
			labels = nil // Tolerate malformed/legacy rows
		}
	}

	var mounts []DockerMount
	if e.Mounts != "" {
		if err := json.Unmarshal([]byte(e.Mounts), &mounts); err != nil {
			mounts = nil
		}
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
		Created:            e.Created,
		FinishedAt:         e.FinishedAt,
		Project:            e.Project,
		Service:            e.Service,
		Labels:             labels,
		ComposeConfigFiles: e.ComposeConfigFiles,
		ComposeWorkingDir:  e.ComposeWorkingDir,
		SizeRw:             e.SizeRw,
		SizeRootFs:         e.SizeRootFs,
		Mounts:             mounts,
		ImageID:            e.ImageID,
		UpdateAvailable:    e.UpdateAvailable,
		UpdateChecked:      e.UpdateChecked,
		LocalDigest:        e.LocalDigest,
		RemoteDigest:       e.RemoteDigest,
		ImageVersion:       e.ImageVersion,
		RemoteVersion:      e.RemoteVersion,
		NewerVersion:       e.NewerVersion,
		NewerMajorVersion:  e.NewerMajorVersion,
	}, nil
}

// ToDockerContainerEntity converts a DockerContainer to a DockerContainerEntity.
// This method is used for database storage preparation.
func (c DockerContainer) ToDockerContainerEntity(metricTimestamp time.Time) (DockerContainerEntity, error) {
	portsJSON, err := json.Marshal(c.Ports)
	if err != nil {
		portsJSON = []byte("[]") // Default to empty array on error
	}

	labelsJSON, err := json.Marshal(c.Labels)
	if err != nil || c.Labels == nil {
		labelsJSON = []byte("{}") // Default to empty object on error/nil
	}

	mountsJSON, err := json.Marshal(c.Mounts)
	if err != nil || c.Mounts == nil {
		mountsJSON = []byte("[]")
	}

	return DockerContainerEntity{
		ID:                 c.ID,
		MetricTimestamp:    metricTimestamp,
		Name:               c.Name,
		Image:              c.Image,
		State:              c.State,
		Status:             c.Status,
		Ports:              string(portsJSON),
		CPUPercent:         c.Stats.CPUPercent,
		CPULimit:           c.Stats.CPULimit,
		CPUPercentOfLimit:  c.Stats.CPUPercentOfLimit,
		MemoryUsage:        c.Stats.MemoryUsage,
		MemoryLimit:        c.Stats.MemoryLimit,
		MemoryPercent:      c.Stats.MemoryPercent,
		NetworkRx:          c.Stats.NetworkRx,
		NetworkTx:          c.Stats.NetworkTx,
		BlockRead:          c.Stats.BlockRead,
		BlockWrite:         c.Stats.BlockWrite,
		Created:            c.Created,
		FinishedAt:         c.FinishedAt,
		Project:            c.Project,
		Service:            c.Service,
		Labels:             string(labelsJSON),
		ComposeConfigFiles: c.ComposeConfigFiles,
		ComposeWorkingDir:  c.ComposeWorkingDir,
		SizeRw:             c.SizeRw,
		SizeRootFs:         c.SizeRootFs,
		Mounts:             string(mountsJSON),
		ImageID:            c.ImageID,
		UpdateAvailable:    c.UpdateAvailable,
		UpdateChecked:      c.UpdateChecked,
		LocalDigest:        c.LocalDigest,
		RemoteDigest:       c.RemoteDigest,
		ImageVersion:       c.ImageVersion,
		RemoteVersion:      c.RemoteVersion,
		NewerVersion:       c.NewerVersion,
		NewerMajorVersion:  c.NewerMajorVersion,
	}, nil
}

// HistoricalDockerMetric represents a historical Docker daemon metric stored in the database.
type HistoricalDockerMetric struct {
	HostID            *uint                   `json:"host_id" gorm:"default:null;index;index:idx_docker_host_ts"`
	Timestamp         time.Time               `json:"timestamp" gorm:"primaryKey;index;index:idx_docker_host_ts"`
	TotalContainers   int                     `json:"total_containers" gorm:"column:total_containers"`
	RunningContainers int                     `json:"running_containers" gorm:"column:running_containers"`
	DockerAvailable   bool                    `json:"docker_available" gorm:"column:docker_available"`
	Containers        []DockerContainerEntity `gorm:"foreignKey:MetricTimestamp"`
}

func (h HistoricalDockerMetric) GetTimestamp() time.Time { return h.Timestamp }
func (h HistoricalDockerMetric) GetMetricType() string   { return "docker" }
func (HistoricalDockerMetric) TableName() string         { return "docker_metrics" }

// DockerRepository defines the interface for Docker metric data operations.
type DockerRepository interface {
	SaveCurrentMetric(ctx context.Context, metric DockerMetric, hostId uint) error
	// SaveCurrentMetricAt persists with an explicit timestamp (Raft applier).
	SaveCurrentMetricAt(ctx context.Context, metric DockerMetric, hostId uint, ts time.Time) error
	GetLatestMetric(ctx context.Context) (DockerMetric, error)
	GetLatestMetricByHost(ctx context.Context, hostId uint) (*DockerMetric, error)
	GetHistoricalMetrics(ctx context.Context, hours float64) ([]HistoricalDockerMetric, error)
	GetHistoricalMetricsByHost(ctx context.Context, hostId uint, hours float64) ([]HistoricalDockerMetric, error)
}

// ContainerLogRef identifies which container's logs to fetch. The stored ID can
// go stale — swarm recreates task containers with a new ID on every restart /
// redeploy — so the collector resolves the *current* live container on the local
// daemon by name, then swarm-service, then compose-service when the ID is gone.
// Resolving against `All` containers also means a stopped (but not removed)
// container's logs remain viewable.
type ContainerLogRef struct {
	ID           string // stored container id (used directly when still live)
	Name         string // stored container name
	Project      string // compose project / swarm service grouping key
	Service      string // service within the application
	SwarmService string // com.docker.swarm.service.name label, if any
}

// DockerMetricsCollector defines the interface for collecting Docker metrics.
type DockerMetricsCollector interface {
	CollectDockerMetrics(ctx context.Context) (DockerMetric, error)
	IsDockerAvailable(ctx context.Context) bool
	// GetContainerLogs returns the last `tail` log lines of a container (stdout +
	// stderr, demuxed), with timestamps. Local daemon only. The container is
	// resolved live from the ref so a recreated/stopped container still works.
	GetContainerLogs(ctx context.Context, ref ContainerLogRef, tail int) (string, error)
	Close() error
}

// ExtractStackNameFromContainerName derives a stack name from a container name
// by splitting on "-" and taking the first segment. Trailing numeric instance
// suffixes (e.g. "-1") are stripped so "stack-service-1" returns "stack".
func ExtractStackNameFromContainerName(containerName string) string {
	if containerName == "" {
		return containerName
	}

	parts := strings.Split(containerName, "-")

	if len(parts) < 2 {
		return containerName
	}

	// Strip trailing instance number (e.g. myapp-web-1 -> myapp)
	if _, err := strconv.Atoi(parts[len(parts)-1]); err == nil {
		if len(parts) >= 3 {
			return strings.Join(parts[:len(parts)-2], "-")
		}
		return parts[0]
	}

	return parts[0]
}
