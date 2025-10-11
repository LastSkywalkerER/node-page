package entities

import "time"

/**
 * HostInfo represents the basic information collected about a host.
 * This structure contains hostname and MAC address for host identification.
 */
type HostInfo struct {
	/** Name is the hostname of the machine */
	Name string `json:"name"`

	/** MacAddress is the MAC address of the primary network interface */
	MacAddress string `json:"mac_address"`

	/** IPv4 is the primary IPv4 address of the host */
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

/**
 * HostHealth represents health check information for a host.
 */
type HostHealth struct {
	/** HostID is the ID of the host */
	HostID uint `json:"host_id"`

	/** Status indicates the health status of the host */
	Status string `json:"status"`

	/** Latency in milliseconds to reach the host */
	Latency float64 `json:"latency_ms"`

	/** Uptime in seconds since the host was last seen */
	Uptime int64 `json:"uptime_seconds"`

	/** LastSeen indicates when the host was last active */
	LastSeen time.Time `json:"last_seen"`
}
