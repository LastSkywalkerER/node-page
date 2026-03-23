package entities

import "time"

 // HealthResponse represents health check information.
type HealthResponse struct {
	// Status indicates the health status
	Status string `json:"status"`

	// Timestamp of the health check
	Timestamp time.Time `json:"timestamp"`

	// Uptime of the server
	Uptime string `json:"uptime"`

	// HostID is the ID of the host (optional, for host-specific health checks)
	HostID uint `json:"host_id,omitempty"`

	// Latency in milliseconds to reach the host (optional)
	Latency float64 `json:"latency_ms,omitempty"`

	// HostUptime session length in seconds for online cluster agents (optional).
	HostUptime int64 `json:"host_uptime_seconds,omitempty"`

	// SessionUptime human-readable session uptime for agents ("3h58m"); empty when unknown or non-agent.
	SessionUptime string `json:"session_uptime,omitempty"`

	// IsClusterAgent is true when this host has a push credential on this server (remote agent).
	IsClusterAgent bool `json:"is_cluster_agent"`

	// LastSeen indicates when the host was last active (optional)
	LastSeen time.Time `json:"last_seen,omitempty"`
}
