package hosts

import "time"

// LocalCollectorHostID is the fixed primary key for the machine where this server collects metrics.
// Remote cluster agents use other rows (Join / UpsertHost). Do not reassign this ID.
const LocalCollectorHostID uint = 1

// HostOfflineThreshold marks a host offline when its last_seen is older than
// this. Every node's local collector refreshes last_seen each metrics cycle
// (~5s) and Raft replicates it cluster-wide, so a single threshold covers both
// the local collector and remote Raft peers.
const HostOfflineThreshold = 45 * time.Second

// HostType values. Empty means unknown (legacy rows / plain agents).
const (
	HostTypeHypervisor = "hypervisor"
	HostTypeVM         = "vm"
	HostTypeLXC        = "lxc"
)

// Source values describe who maintains a host row. Empty is treated as
// SourceAgent (legacy rows created before connectors existed).
const (
	SourceAgent          = "agent"
	SourceConnector      = "connector"
	SourceAgentConnector = "agent+connector"
)

// MergeSource combines an existing source with a newly observed one so a row
// fed by both an agent and a connector converges to SourceAgentConnector.
func MergeSource(existing, incoming string) string {
	if existing == "" {
		existing = SourceAgent
	}
	if incoming == "" || existing == incoming || existing == SourceAgentConnector {
		return existing
	}
	return SourceAgentConnector
}

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
	// HardwareUUID is the SMBIOS product UUID (/sys/class/dmi/id/product_uuid,
	// lowercased). Inside a QEMU VM it equals the guest's `smbios1` UUID in
	// its PVE config, so connectors can link a VM whose registered MAC the
	// hypervisor has never seen (agent behind a Docker bridge). Empty when
	// unreadable (non-root native installs).
	HardwareUUID string `json:"hardware_uuid,omitempty" gorm:"index"`

	// Virtualization topology (filled by connectors, e.g. Proxmox).
	// HostType is "" (unknown) | hypervisor | vm | lxc.
	HostType string `json:"host_type,omitempty"`
	// ParentMAC is the MAC of the hypervisor host row this guest runs on.
	// MAC (not local row id) because Raft identity is MAC-based — local
	// autoincrement ids differ per node. "" = top-level host.
	ParentMAC string `json:"parent_mac,omitempty" gorm:"index"`
	// Source is "" / agent (legacy default) | connector | agent+connector.
	Source string `json:"source,omitempty"`
	// ExternalID is the stable connector-side identity,
	// e.g. "pve:<fingerprint>/<node>/qemu/<vmid>".
	ExternalID string `json:"external_id,omitempty" gorm:"index"`
	// GuestStatus is the hypervisor-reported power state (running | stopped |
	// paused). Only set for connector-managed guests; "" for plain agents.
	GuestStatus string `json:"guest_status,omitempty"`
	// OriginCluster is the uplink site this row arrived from over the
	// cross-cluster bridge ("home", "office"); "" for rows of this cluster.
	OriginCluster string `json:"origin_cluster,omitempty" gorm:"index"`

	// ParentID is the LOCAL row id of the ParentMAC host, resolved at read
	// time for the UI (not a DB column — ids differ per node).
	ParentID uint `json:"parent_id,omitempty" gorm:"-"`

	// BootTime is the host's boot time in Unix seconds. Replicated cluster-wide
	// so any node can show this host's real system uptime (now - boot_time).
	BootTime int64 `json:"boot_time"`

	// LastSeen indicates when this host was last active
	LastSeen time.Time `json:"last_seen"`

	// AgentSessionStartedAt is set on cluster agents: start of current "online" session after a push gap (>30s). Nil for non-agents or before first push.
	AgentSessionStartedAt *time.Time `json:"agent_session_started_at,omitempty"`

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
	// OriginCluster marks rows arriving over the cross-cluster bridge (set
	// by the applier from the command envelope, not collected).
	OriginCluster string `json:"origin_cluster,omitempty"`

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
	// HardwareUUID is the SMBIOS product UUID (see Host.HardwareUUID).
	HardwareUUID string `json:"hardware_uuid,omitempty"`
	// BootTime is the host's boot time in Unix seconds (from gopsutil). Stable
	// per boot; used to derive the host's real system uptime on any node.
	BootTime int64 `json:"boot_time"`
}

// ConnectorHostInfo is the host info a connector (e.g. the Proxmox poller)
// publishes for hypervisor nodes and agent-less guests. It extends the plain
// agent HostInfo with topology fields.
type ConnectorHostInfo struct {
	HostInfo
	HostType    string `json:"host_type,omitempty"`
	ParentMAC   string `json:"parent_mac,omitempty"`
	ExternalID  string `json:"external_id,omitempty"`
	GuestStatus string `json:"guest_status,omitempty"`
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
