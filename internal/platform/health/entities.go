package health

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

	// HostUptime is the host's system uptime in seconds (0 when its boot time is unknown).
	HostUptime int64 `json:"host_uptime_seconds,omitempty"`

	// LastSeen indicates when the host was last active (optional)
	LastSeen time.Time `json:"last_seen,omitempty"`
}
