package hosts

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

// IdentityFreezeGrace is how long after a host row's creation a connector may
// still freely rewrite its identity fields (name, MAC): discovery needs a few
// cycles to settle (MAC resolution, agent linking, dedup). Past the grace
// window identity updates are frozen — the connector proposes them as a
// HostPendingChange that an admin approves instead of overwriting the row on
// every poll cycle (and replicating that churn cluster-wide).
const IdentityFreezeGrace = 10 * time.Minute

// HostPendingChange statuses.
const (
	PendingStatusPending  = "pending"
	PendingStatusRejected = "rejected"
)

// PendingFieldChange is one frozen identity-field update awaiting approval.
type PendingFieldChange struct {
	Field string `json:"field"` // "name" | "mac_address"
	Old   string `json:"old"`
	New   string `json:"new"`
}

// HostPendingChange is a connector-proposed identity update to an EXISTING
// host row, parked for admin approval instead of being auto-applied. One row
// per (host MAC, source) — repeated polls converge onto the same row via the
// deterministic ChangeID, so an unresolved proposal is a single replicated
// upsert, not a stream. Status "rejected" keeps the fingerprint so the source
// stops re-proposing the same value until it changes again.
type HostPendingChange struct {
	ID uint `json:"-" gorm:"primaryKey;autoIncrement"`

	// ChangeID is the cluster-stable identity: hex(sha256(mac|source))[:16].
	// Local autoincrement ids differ per node, so Raft commands and the API
	// key on this instead.
	ChangeID string `json:"change_id" gorm:"uniqueIndex;not null"`

	// HostMAC identifies the target host row (the cluster-wide host key).
	HostMAC string `json:"host_mac" gorm:"index;not null"`
	// HostName is the row's display name at proposal time (UI label only).
	HostName string `json:"host_name"`
	// Source is the proposing connector type ("proxmox" | "pbs").
	Source string `json:"source"`

	// Changes is the JSON-encoded []PendingFieldChange.
	Changes string `json:"-" gorm:"type:text"`
	// Fingerprint hashes the proposed new values so an unchanged proposal is
	// never resubmitted (and a rejected one stays rejected until the source
	// value changes).
	Fingerprint string `json:"fingerprint"`
	// Status is pending | rejected.
	Status string `json:"status"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	// FieldChanges is the decoded Changes for API responses (not a column).
	FieldChanges []PendingFieldChange `json:"changes" gorm:"-"`
}

// TableName returns the database table name for GORM operations.
func (HostPendingChange) TableName() string { return "host_pending_changes" }

// DecodeFieldChanges fills FieldChanges from the stored JSON.
func (c *HostPendingChange) DecodeFieldChanges() {
	if c.Changes == "" {
		c.FieldChanges = nil
		return
	}
	if err := json.Unmarshal([]byte(c.Changes), &c.FieldChanges); err != nil {
		c.FieldChanges = nil
	}
}

// PendingChangeID derives the cluster-stable id for the (host, source) pair.
func PendingChangeID(mac, source string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(mac) + "|" + source))
	return fmt.Sprintf("%x", sum)[:16]
}

// PendingChangesFingerprint hashes the proposed NEW values (sorted by field)
// so equality means "the source still proposes exactly this".
func PendingChangesFingerprint(changes []PendingFieldChange) string {
	sorted := make([]PendingFieldChange, len(changes))
	copy(sorted, changes)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Field < sorted[j].Field })
	parts := make([]string, 0, len(sorted))
	for _, ch := range sorted {
		parts = append(parts, ch.Field+"="+ch.New)
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return fmt.Sprintf("%x", sum)
}

var realMACRe = regexp.MustCompile(`^([0-9a-fA-F]{2}:){5}[0-9a-fA-F]{2}$`)

// IsRealMAC reports whether s looks like an actual hardware MAC (connector
// rows without a readable NIC carry a synthetic external-id placeholder or a
// derived b2:… locally-administered address instead).
func IsRealMAC(s string) bool { return realMACRe.MatchString(s) }

// nameEquivalent treats the repository's uniqueness suffix ("media-vm
// [pve/qemu/105]") as equal to the bare desired name, so it never reads as an
// identity change.
func nameEquivalent(existingName, desired string) bool {
	return existingName == desired || strings.HasPrefix(existingName, desired+" [")
}

// FreezeIdentity implements the identity freeze for connector writes onto an
// EXISTING host row: past the grace window, a connector-owned row's identity
// fields (name, MAC) are no longer rewritten in place — the returned info has
// them pinned to the row's current values, and the detected diffs come back as
// pending field changes for admin approval.
//
// Only name and mac_address are frozen. Topology (parent_mac, host_type,
// external_id) and state (guest_status, boot_time, IP) keep flowing: a VM
// migration in PVE legitimately changes parent + external_id every time, and
// freezing external_id would make the prune sweep delete the row.
//
// Agent-maintained rows (agent / agent+connector) are returned unchanged: the
// repository already limits connector writes there to topology columns, so
// their identity is never connector-written in the first place.
func FreezeIdentity(existing *Host, info ConnectorHostInfo, now time.Time) (ConnectorHostInfo, []PendingFieldChange) {
	if existing == nil {
		return info, nil // creation — free
	}
	if MergeSource(existing.Source, SourceConnector) != SourceConnector {
		return info, nil // agent-owned — connector never writes identity here
	}

	frozen := info
	var changes []PendingFieldChange

	// A synthetic incoming MAC (NIC unreadable this cycle / placeholder) must
	// never displace a stored identity — keep the row's MAC silently, it is
	// a degraded read rather than a change. A real incoming MAC that differs
	// from the stored one is a genuine identity change → freeze + propose.
	if info.MacAddress != "" && !strings.EqualFold(info.MacAddress, existing.MacAddress) {
		frozen.MacAddress = existing.MacAddress
		if IsRealMAC(info.MacAddress) && now.Sub(existing.CreatedAt) >= IdentityFreezeGrace {
			changes = append(changes, PendingFieldChange{
				Field: "mac_address",
				Old:   existing.MacAddress,
				New:   strings.ToLower(info.MacAddress),
			})
		} else if now.Sub(existing.CreatedAt) < IdentityFreezeGrace {
			frozen.MacAddress = info.MacAddress // grace window — apply freely
		}
	}

	if now.Sub(existing.CreatedAt) < IdentityFreezeGrace {
		return frozen, nil // grace window — name applies freely, no proposals
	}

	if info.Name != "" && !nameEquivalent(existing.Name, info.Name) {
		frozen.Name = existing.Name
		changes = append(changes, PendingFieldChange{
			Field: "name",
			Old:   existing.Name,
			New:   info.Name,
		})
	}

	return frozen, changes
}
