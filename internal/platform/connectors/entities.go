// Package connectors implements the external data-source registry ("connectors").
//
// A connector is a configured integration that enriches the host registry with
// machines node-stats has no agent on — the first implementation is the
// Proxmox VE API (see internal/metrics/proxmox). The package owns:
//
//   - the replicated Connector entity (credentials encrypted at rest),
//   - credential-free environment detection ("looks like we run inside
//     Proxmox") surfaced as DiscoveredHint,
//   - the admin CRUD service + HTTP handler.
//
// Connector rows replicate via Raft (CmdConnectorUpsert/Delete) so any node
// can take over polling after a failover; only the runtime status columns
// (status/last_sync_at/last_error) are maintained locally by the polling node.
package connectors

import "time"

// Connector types.
const (
	TypeProxmox = "proxmox"
)

// Poller status values (local, non-replicated).
const (
	StatusOK          = "ok"
	StatusUnreachable = "unreachable"
	StatusAuthFailed  = "auth_failed"
	StatusDisabled    = "disabled"
	StatusPending     = "pending"
)

// Connector is a configured external data source. Credentials are stored as
// AES-GCM ciphertext under a key derived from the cluster-shared JWT secret,
// because rows replicate through the Raft log and snapshots.
type Connector struct {
	ID   uint   `json:"id" gorm:"primaryKey;autoIncrement"`
	Type string `json:"type" gorm:"index;not null"`

	// Endpoint is the API base URL, e.g. https://192.168.1.2:8006.
	Endpoint string `json:"endpoint"`
	// TokenID is the API token id (Proxmox: user@realm!tokenid).
	TokenID string `json:"token_id"`
	// SecretEnc is the encrypted token secret. Never serialized to the API.
	SecretEnc     []byte `json:"-"`
	SkipTLSVerify bool   `json:"skip_tls_verify"`

	// Fingerprint is the connector-side cluster identity (e.g. PVE cluster
	// name) — the cluster-wide dedup key.
	Fingerprint string `json:"fingerprint" gorm:"uniqueIndex;not null"`

	Enabled bool `json:"enabled"`

	// Runtime poller state — maintained by the polling node only (NOT
	// replicated; followers may show a stale value until they lead).
	Status     string    `json:"status"`
	LastSyncAt time.Time `json:"last_sync_at"`
	LastError  string    `json:"last_error"`

	CreatedAt time.Time `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt time.Time `json:"updated_at" gorm:"autoUpdateTime"`
}

// TableName returns the database table name for GORM operations.
func (Connector) TableName() string { return "connectors" }

// ExternalIDPrefix is the prefix every host row fed by this connector carries
// in hosts.external_id ("<type>:<fingerprint>/..."). Used to unlink/cascade
// on connector removal.
func ExternalIDPrefix(connectorType, fingerprint string) string {
	return connectorType + ":" + fingerprint + "/"
}

// DiscoveredHint is a credential-free detection result: "this machine looks
// like a guest of <type>". It is a hint with evidence, not a fact — the admin
// confirms it by configuring the connector.
type DiscoveredHint struct {
	Type string `json:"type"` // TypeProxmox
	// Confidence is "high" (Proxmox-specific marker found) or "medium"
	// (generic QEMU/LXC markers only).
	Confidence string `json:"confidence"`
	// GuestKind is "vm" or "lxc".
	GuestKind string `json:"guest_kind"`
	// VMIDHint is this machine's Proxmox VMID when extractable (LXC cgroup).
	VMIDHint int `json:"vmid_hint,omitempty"`
	// SuggestedEndpoint pre-fills the connect form (default gateway :8006).
	SuggestedEndpoint string   `json:"suggested_endpoint,omitempty"`
	Evidence          []string `json:"evidence"`
}

// ProbeResult is what a connector-type prober (e.g. the Proxmox client)
// returns when validating credentials. GuestMACs lets the service compute how
// many discovered guests match already-registered hosts.
type ProbeResult struct {
	Fingerprint string   `json:"fingerprint"`
	ClusterName string   `json:"cluster_name"`
	Version     string   `json:"version"`
	Nodes       []string `json:"nodes"`
	GuestCount  int      `json:"guest_count"`
	GuestMACs   []string `json:"-"`
}

// Preview is the test-connection response shown before saving.
type Preview struct {
	ProbeResult
	MatchedHosts int `json:"matched_hosts"`
}
