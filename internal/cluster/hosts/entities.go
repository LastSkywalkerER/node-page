package hosts

import "time"

// LocalCollectorHostID is the fixed primary key for the machine where this server collects metrics.
// Remote cluster agents use other rows (Join / UpsertHost). Do not reassign this ID.
const LocalCollectorHostID uint = 1

const (
	// AgentPushGapSessionReset starts a new agent session if last push was longer ago than this.
	AgentPushGapSessionReset = 30 * time.Second
	// AgentOfflineThreshold marks a cluster agent offline if last push is older than this.
	AgentOfflineThreshold = 45 * time.Second
	// LocalHostOfflineThreshold for hosts without node credentials (local collector only).
	LocalHostOfflineThreshold = 5 * time.Minute
)

// Host represents a host machine identified by its MAC address.
// This structure contains information about the host's name and MAC address,
// used for tracking metrics from different hosts.
type Host struct {
	// ID is the unique identifier for the host
	ID uint `json:"id" gorm:"primaryKey;autoIncrement"`

	// Name is the hostname of the machine
	Name string `json:"name" gorm:"uniqueIndex;not null"`

	// MacAddress is the MAC address of the primary network interface
	MacAddress string `json:"mac_address" gorm:"uniqueIndex;not null"`

	// IPv4 is the primary IPv4 address of the host
	IPv4 string `json:"ipv4" gorm:"index"`

	// Extended system info
	OS                   string `json:"os"`
	Platform             string `json:"platform"`
	PlatformFamily       string `json:"platform_family"`
	PlatformVersion      string `json:"platform_version"`
	KernelVersion        string `json:"kernel_version"`
	VirtualizationSystem string `json:"virtualization_system"`
	VirtualizationRole   string `json:"virtualization_role"`
	SystemHostID         string `json:"system_host_id"`

	// LastSeen indicates when this host was last active
	LastSeen time.Time `json:"last_seen"`

	// AgentSessionStartedAt is set on cluster agents: start of current "online" session after a push gap (>30s). Nil for non-agents or before first push.
	AgentSessionStartedAt *time.Time `json:"agent_session_started_at,omitempty"`

	// HasNodeCredential is set when listing hosts: this host can push to main (not a DB column).
	HasNodeCredential bool `json:"has_node_credential" gorm:"-"`

	// DisplayName is set for the local collector row when NODE_STATS_HOSTNAME is set (not a DB column).
	// UI uses it for the machine card title; when empty, the card omits the title for host id 1.
	DisplayName string `json:"display_name,omitempty" gorm:"-"`

	// CreatedAt indicates when this host record was created
	CreatedAt time.Time `json:"created_at" gorm:"autoCreateTime"`

	// UpdatedAt indicates when this host record was last updated
	UpdatedAt time.Time `json:"updated_at" gorm:"autoUpdateTime"`
}

// TableName returns the database table name for GORM operations.
func (Host) TableName() string { return "hosts" }

// HostInfo represents the basic information collected about a host.
// This structure contains hostname and MAC address for host identification.
type HostInfo struct {
	// Name is the hostname of the machine
	Name string `json:"name"`

	// MacAddress is the MAC address of the primary network interface
	MacAddress string `json:"mac_address"`

	// IPv4 is the primary IPv4 address of the host
	IPv4 string `json:"ipv4"`

	// Extended host/system info
	OS                   string `json:"os"`
	Platform             string `json:"platform"`
	PlatformFamily       string `json:"platform_family"`
	PlatformVersion      string `json:"platform_version"`
	KernelVersion        string `json:"kernel_version"`
	VirtualizationSystem string `json:"virtualization_system"`
	VirtualizationRole   string `json:"virtualization_role"`
	HostID               string `json:"host_id"`
}

// HostHealth represents health check information for a host.
type HostHealth struct {
	// HostID is the ID of the host
	HostID uint `json:"host_id"`

	// Status indicates the health status of the host
	Status string `json:"status"`

	// Latency in milliseconds to reach the host
	Latency float64 `json:"latency_ms"`

	// Uptime in seconds since the host was last seen
	Uptime int64 `json:"uptime_seconds"`

	// LastSeen indicates when the host was last active
	LastSeen time.Time `json:"last_seen"`
}
