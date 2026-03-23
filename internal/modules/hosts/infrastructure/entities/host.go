package entities

import (
	"time"
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

	// CreatedAt indicates when this host record was created
	CreatedAt time.Time `json:"created_at" gorm:"autoCreateTime"`

	// UpdatedAt indicates when this host record was last updated
	UpdatedAt time.Time `json:"updated_at" gorm:"autoUpdateTime"`
}

 // TableName returns the database table name for GORM operations.
func (Host) TableName() string { return "hosts" }
