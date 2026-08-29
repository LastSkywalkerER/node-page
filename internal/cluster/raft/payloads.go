package raft

import (
	"encoding/json"
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
	HardwareUUID         string `json:"hardware_uuid,omitempty"`
	BootTime             int64  `json:"boot_time,omitempty"`
}

// ConnectorHostUpsertPayload mirrors hosts.ConnectorHostInfo: a hypervisor
// node or agent-less guest discovered by a connector. The applier calls
// hostRepo.UpsertConnectorHost, which links to an existing agent row by MAC
// instead of duplicating it.
type ConnectorHostUpsertPayload struct {
	HostUpsertPayload
	HostType    string `json:"host_type,omitempty"`
	ParentMAC   string `json:"parent_mac,omitempty"`
	ExternalID  string `json:"external_id,omitempty"`
	GuestStatus string `json:"guest_status,omitempty"`
}

// ConnectorUpsertPayload replicates a configured connector (credentials are
// AES-GCM ciphertext under the cluster-shared JWT secret) so any node can
// take over polling. Keyed by Fingerprint — the connector-side identity.
type ConnectorUpsertPayload struct {
	Type          string `json:"type"`
	Endpoint      string `json:"endpoint"`
	TokenID       string `json:"token_id"`
	SecretEnc     []byte `json:"secret_enc"`
	SkipTLSVerify bool   `json:"skip_tls_verify"`
	Fingerprint   string `json:"fingerprint"`
	Config        string `json:"config,omitempty"`
	Enabled       bool   `json:"enabled"`
}

// ConnectorDeletePayload removes a connector everywhere. RemoveHosts also
// cascades the connector-only host rows it created; linked agent rows are
// only unlinked either way.
type ConnectorDeletePayload struct {
	Type        string `json:"type"`
	Fingerprint string `json:"fingerprint"`
	RemoveHosts bool   `json:"remove_hosts"`
}

// GatewayRouteUpsertPayload replicates one gateway route, keyed by RouteID (the
// cluster-stable identity; local autoincrement ids differ per node). Basic-auth
// entries are already bcrypt hashes.
type GatewayRouteUpsertPayload struct {
	RouteID                  string `json:"route_id"`
	Name                     string `json:"name"`
	Domain                   string `json:"domain"`
	PathPrefix               string `json:"path_prefix,omitempty"`
	TargetScheme             string `json:"target_scheme"`
	TargetHost               string `json:"target_host"`
	TargetPort               int    `json:"target_port"`
	TargetHostMAC            string `json:"target_host_mac,omitempty"`
	TargetLabel              string `json:"target_label,omitempty"`
	TargetInsecureSkipVerify bool   `json:"target_insecure_skip_verify"`
	TLS                      bool   `json:"tls"`
	BasicAuthUsers           string `json:"basic_auth_users,omitempty"`
	IPAllowList              string `json:"ip_allow_list,omitempty"`
	Enabled                  bool   `json:"enabled"`
}

// GatewayRouteDeletePayload removes a gateway route everywhere.
type GatewayRouteDeletePayload struct {
	RouteID string `json:"route_id"`
}

// HostDeletePayload cascades a host removal (row + all its metrics) across the
// cluster. It is keyed by MAC — the stable, cluster-wide identity — because the
// per-node host_id differs on every node (auto-increment, matched by MAC). Each
// node resolves the MAC to its own local row id before cascading.
type HostDeletePayload struct {
	HostMAC string `json:"host_mac"`
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

// BridgeEnvelope is one remote command inside a CmdBridgeEnvelopeBatch.
type BridgeEnvelope struct {
	Type            CommandType     `json:"type"`
	OriginClusterID string          `json:"origin_cluster_id"`
	OriginNodeID    string          `json:"origin_node_id"`
	OriginIndex     uint64          `json:"origin_index"`
	Timestamp       time.Time       `json:"ts"`
	Payload         json.RawMessage `json:"payload"`
}

// BridgeEnvelopeBatchPayload carries a whole replication batch in one log
// entry (one consensus round + one fsync instead of one per envelope).
type BridgeEnvelopeBatchPayload struct {
	Entries []BridgeEnvelope `json:"entries"`
}

// UserUpsertPayload mirrors the persisted fields of users.User. Used both for
// initial registration (when Role=ADMIN) and later role updates.
type UserUpsertPayload struct {
	ID           uint   `json:"id,omitempty"` // 0 on initial Create; populated on UpdateRole
	Email        string `json:"email"`
	PasswordHash string `json:"password_hash,omitempty"`
	Role         string `json:"role"`
}

// UserPasswordChangePayload replicates a password change for an existing user.
// Keyed by email (the stable cluster-wide identity — local row ids differ per
// node), carrying only the new bcrypt hash.
type UserPasswordChangePayload struct {
	Email        string `json:"email"`
	PasswordHash string `json:"password_hash"`
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

// MetricBatchPayload carries one host's current metrics for cluster-wide
// replication. The host is identified by a stable key (MAC, name as fallback)
// because the local autoincrement host id differs per node — the applier
// resolves it to the local hosts row. Each module's metric is the full entity
// JSON (the same shape the API returns); the applier unmarshals it and hands
// it to that module's repository, which keeps its existing entity→historical
// mapping. Timestamp is the origin's collection time so every replica stores
// the row under the same instant.
type MetricBatchPayload struct {
	HostMAC  string `json:"host_mac"`
	HostName string `json:"host_name,omitempty"`
	// HostExternalID is the stable connector identity (e.g. "pbs:<fp>/<node>"),
	// used to resolve connector nodes that expose no NIC MAC (PBS) on a remote
	// cluster, where the synthetic MAC is regenerated and won't match.
	HostExternalID string          `json:"host_external_id,omitempty"`
	Timestamp      time.Time       `json:"ts"`
	CPU            json.RawMessage `json:"cpu,omitempty"`
	Memory         json.RawMessage `json:"memory,omitempty"`
	Disk           json.RawMessage `json:"disk,omitempty"`
	Network        json.RawMessage `json:"network,omitempty"`
	Docker         json.RawMessage `json:"docker,omitempty"`
	// PBS carries a Proxmox Backup Server detail snapshot (datastores/backup
	// groups/health) for this host, so non-polling nodes and bridged hub
	// clusters can serve GET /pbs. Opaque JSON (the pbs.Status entity).
	PBS json.RawMessage `json:"pbs,omitempty"`
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
