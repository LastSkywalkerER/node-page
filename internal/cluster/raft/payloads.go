package raft

import (
	"time"
)

// Concrete payload structs for each CommandType. They are JSON-encoded into
// Command.Payload by the submitter and decoded by the applier. Field names
// match the existing GORM entities where possible so the appliers can map
// 1:1 without extra translation.

// HostUpsertPayload mirrors hosts.HostInfo so the applier can call
// hostRepo.UpsertHost(...) directly.
type HostUpsertPayload struct {
	Name                 string `json:"name"`
	MacAddress           string `json:"mac_address"`
	IPv4                 string `json:"ipv4"`
	OS                   string `json:"os"`
	Platform             string `json:"platform"`
	PlatformFamily       string `json:"platform_family"`
	PlatformVersion      string `json:"platform_version"`
	KernelVersion        string `json:"kernel_version"`
	VirtualizationSystem string `json:"virtualization_system"`
	VirtualizationRole   string `json:"virtualization_role"`
	HostID               string `json:"host_id"`
}

// HostDeletePayload cascades through metrics tables. Matches
// hosts.Repository.DeleteHostCascade.
type HostDeletePayload struct {
	HostID uint `json:"host_id"`
}

// HostLastSeenPayload mirrors hosts.Repository.UpdateLastSeenAndAgentSession.
// AgentSessionStartedAt is optional (nil when push gap < threshold).
type HostLastSeenPayload struct {
	HostID                uint       `json:"host_id"`
	LastSeen              time.Time  `json:"last_seen"`
	AgentSessionStartedAt *time.Time `json:"agent_session_started_at,omitempty"`
	Name                  string     `json:"name,omitempty"`
	IPv4                  string     `json:"ipv4,omitempty"`
}

// UserUpsertPayload mirrors the persisted fields of users.User. Used both for
// initial registration (when Role=ADMIN) and later role updates.
type UserUpsertPayload struct {
	ID           uint   `json:"id,omitempty"` // 0 on initial Create; populated on UpdateRole
	Email        string `json:"email"`
	PasswordHash string `json:"password_hash,omitempty"`
	Role         string `json:"role"`
}

// UserDeletePayload removes a user by ID.
type UserDeletePayload struct {
	UserID uint `json:"user_id"`
}

// RefreshTokenIssuePayload mirrors the persisted fields of users.RefreshToken.
type RefreshTokenIssuePayload struct {
	UserID    uint      `json:"user_id"`
	JTI       string    `json:"jti"`
	TokenHash string    `json:"token_hash"`
	ExpiresAt time.Time `json:"expires_at"`
	CreatedAt time.Time `json:"created_at"`
}

// RefreshTokenRevokePayload covers both RevokeByJTI and ConsumeByJTI (the
// applier always performs an idempotent revoke; the "consume" winner is the
// node that submitted the command — its SubmitResult.Err is nil — others
// receive a "already revoked" no-op).
type RefreshTokenRevokePayload struct {
	JTI    string `json:"jti,omitempty"`
	UserID uint   `json:"user_id,omitempty"` // when set, revokes all tokens for the user
}

// AuthSecretSetPayload distributes the cluster-shared JWT signing keys so
// that an access token minted on node A is accepted on node B. Stored under
// keys "jwt_secret" and "refresh_secret" in cluster_config.
type AuthSecretSetPayload struct {
	JWTSecret     string `json:"jwt_secret"`
	RefreshSecret string `json:"refresh_secret"`
}

// ConfigSetPayload sets a single key in the replicated cluster_config table.
type ConfigSetPayload struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// PeerNodeAdvertisePayload publishes a node's advertised URL into the URL
// catalog. The cross-cluster bridge / URL picker reads these entries to
// decide where to send replication batches.
type PeerNodeAdvertisePayload struct {
	ClusterID    string   `json:"cluster_id"`
	NodeID       string   `json:"node_id"`
	URL          string   `json:"url"`
	Capabilities []string `json:"capabilities,omitempty"`
}

// PeerNodeRemovePayload removes a node from the URL catalog.
type PeerNodeRemovePayload struct {
	ClusterID string `json:"cluster_id"`
	NodeID    string `json:"node_id"`
}

// MetricBatchPayload bundles every metric module's snapshot for a single
// host at a single timestamp. The applier dispatches each sub-snapshot to
// its corresponding repository's SaveCurrentMetric method.
type MetricBatchPayload struct {
	HostID    uint               `json:"host_id"`
	Timestamp time.Time          `json:"ts"`
	CPU       *CPUSnapshot       `json:"cpu,omitempty"`
	Memory    *MemorySnapshot    `json:"memory,omitempty"`
	Disk      *DiskSnapshot      `json:"disk,omitempty"`
	Network   *NetworkSnapshot   `json:"network,omitempty"`
	Docker    *DockerSnapshot    `json:"docker,omitempty"`
}

// CPUSnapshot is the persisted slice of cpu.CPUMetric.
type CPUSnapshot struct {
	UsagePercent float64 `json:"usage_percent"`
	Cores        int     `json:"cores"`
	LoadAvg1     float64 `json:"load_avg_1"`
	LoadAvg5     float64 `json:"load_avg_5"`
	LoadAvg15    float64 `json:"load_avg_15"`
	Temperature  float64 `json:"temperature"`
}

// MemorySnapshot is the persisted slice of memory.MemoryMetric.
type MemorySnapshot struct {
	UsagePercent float64 `json:"usage_percent"`
	UsedBytes    uint64  `json:"used_bytes"`
	TotalBytes   uint64  `json:"total_bytes"`
}

// DiskSnapshot is the persisted slice of disk.DiskMetric.
type DiskSnapshot struct {
	UsagePercent float64 `json:"usage_percent"`
	UsedBytes    uint64  `json:"used_bytes"`
	TotalBytes   uint64  `json:"total_bytes"`
}

// NetworkSnapshot — interfaces are serialised as raw JSON because the
// underlying struct lives in the network package; the applier hands the
// raw bytes back to the network repo which knows how to decode them.
type NetworkSnapshot struct {
	InterfacesJSON []byte `json:"interfaces_json"`
}

// DockerSnapshot mirrors docker.DockerMetric.
type DockerSnapshot struct {
	TotalContainers   int                `json:"total_containers"`
	RunningContainers int                `json:"running_containers"`
	DockerAvailable   bool               `json:"docker_available"`
	Containers        []DockerContainer  `json:"containers,omitempty"`
}

// DockerContainer mirrors docker.DockerContainerEntity persisted fields.
type DockerContainer struct {
	ID                string  `json:"id"`
	Name              string  `json:"name"`
	Image             string  `json:"image"`
	State             string  `json:"state"`
	Status            string  `json:"status"`
	Ports             string  `json:"ports,omitempty"`
	CPUPercent        float64 `json:"cpu_percent"`
	CPULimit          float64 `json:"cpu_limit"`
	CPUPercentOfLimit float64 `json:"cpu_percent_of_limit"`
	MemoryUsage       uint64  `json:"memory_usage"`
	MemoryLimit       uint64  `json:"memory_limit"`
	MemoryPercent     float64 `json:"memory_percent"`
	NetworkRx         uint64  `json:"network_rx"`
	NetworkTx         uint64  `json:"network_tx"`
	BlockRead         uint64  `json:"block_read"`
	BlockWrite        uint64  `json:"block_write"`
	Created           string  `json:"created"`
	FinishedAt        string  `json:"finished_at"`
}

// RetentionDeleteBeforePayload tells every replica to delete metric rows
// with timestamp < CutoffTS. Submitted by the leader only on each 5s tick.
type RetentionDeleteBeforePayload struct {
	CutoffTS time.Time `json:"cutoff_ts"`
}

// JoinTokenIssuePayload — one-shot token created by an admin for bringing
// up a new node. TokenHash is hex(SHA256(plaintext)); plaintext is returned
// to the admin once and never stored.
type JoinTokenIssuePayload struct {
	TokenHash string    `json:"token_hash"`
	ExpiresAt time.Time `json:"expires_at"`
	CreatedBy uint      `json:"created_by,omitempty"`
}

// JoinTokenConsumePayload marks a join token used. Idempotent: second use
// is a no-op for replication purposes but rejected by the join endpoint.
type JoinTokenConsumePayload struct {
	TokenHash  string `json:"token_hash"`
	ByNodeID   string `json:"by_node_id"`
	ByNodeAddr string `json:"by_node_addr"`
}

// BridgeAckPayload records the highest OriginIndex from PeerClusterID that
// the local cluster has applied. Persisted inside the FSM so the bridge
// sender resumes from the right offset after restart / leader change.
type BridgeAckPayload struct {
	PeerClusterID   string `json:"peer_cluster_id"`
	LastOriginIndex uint64 `json:"last_origin_index"`
}
