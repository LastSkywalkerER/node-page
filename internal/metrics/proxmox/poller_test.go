package proxmox

import (
	"testing"
	"time"

	hosts "system-stats/internal/cluster/hosts"
)

func newThrottlePoller() *Poller {
	return &Poller{upsertState: map[string]hostUpsertState{}}
}

func guestInfo(externalID, status string) hosts.ConnectorHostInfo {
	return hosts.ConnectorHostInfo{
		HostInfo: hosts.HostInfo{
			Name:       "guest-100",
			MacAddress: "aa:bb:cc:dd:ee:ff",
		},
		HostType:    hosts.HostTypeLXC,
		ExternalID:  externalID,
		GuestStatus: status,
	}
}

func TestShouldSubmitHostUpsert_FirstSubmitAlways(t *testing.T) {
	p := newThrottlePoller()
	now := time.Now()
	if !p.shouldSubmitHostUpsert(guestInfo("pve/lxc/100", "running"), now) {
		t.Fatal("first submission for a host must always go through")
	}
}

func TestShouldSubmitHostUpsert_UnchangedWithinHeartbeatSkips(t *testing.T) {
	p := newThrottlePoller()
	now := time.Now()
	info := guestInfo("pve/lxc/100", "running")

	if !p.shouldSubmitHostUpsert(info, now) {
		t.Fatal("first submission must go through")
	}
	// Same fingerprint, one poll (~10s) later: must be throttled.
	if p.shouldSubmitHostUpsert(info, now.Add(pollInterval)) {
		t.Fatal("unchanged host within heartbeat must be skipped")
	}
	// Still within the heartbeat window just below 60s: skipped.
	if p.shouldSubmitHostUpsert(info, now.Add(hostUpsertHeartbeat-time.Second)) {
		t.Fatal("unchanged host just under heartbeat must be skipped")
	}
}

func TestShouldSubmitHostUpsert_HeartbeatRefresh(t *testing.T) {
	p := newThrottlePoller()
	now := time.Now()
	info := guestInfo("pve/lxc/100", "running")

	if !p.shouldSubmitHostUpsert(info, now) {
		t.Fatal("first submission must go through")
	}
	// Exactly at the heartbeat: a liveness refresh must fire so peers keep the
	// guest online.
	if !p.shouldSubmitHostUpsert(info, now.Add(hostUpsertHeartbeat)) {
		t.Fatal("heartbeat interval elapsed: must refresh last_seen via a submit")
	}
}

func TestShouldSubmitHostUpsert_StatusTransitionReplicatesPromptly(t *testing.T) {
	p := newThrottlePoller()
	now := time.Now()

	if !p.shouldSubmitHostUpsert(guestInfo("pve/lxc/100", "running"), now) {
		t.Fatal("first submission must go through")
	}
	// A guest going stopped within the heartbeat window must NOT be throttled —
	// status is part of the fingerprint, so the transition replicates next poll.
	if !p.shouldSubmitHostUpsert(guestInfo("pve/lxc/100", "stopped"), now.Add(pollInterval)) {
		t.Fatal("guest_status change must bypass the throttle")
	}
	// And back to running, again within the window, also submits.
	if !p.shouldSubmitHostUpsert(guestInfo("pve/lxc/100", "running"), now.Add(2*pollInterval)) {
		t.Fatal("guest_status change back must bypass the throttle")
	}
}

func TestShouldSubmitHostUpsert_TopologyChangeBypassesThrottle(t *testing.T) {
	p := newThrottlePoller()
	now := time.Now()

	base := guestInfo("pve/lxc/100", "running")
	if !p.shouldSubmitHostUpsert(base, now) {
		t.Fatal("first submission must go through")
	}
	moved := base
	moved.ParentMAC = "11:22:33:44:55:66" // migrated to another hypervisor
	if !p.shouldSubmitHostUpsert(moved, now.Add(pollInterval)) {
		t.Fatal("topology (parent_mac) change must bypass the throttle")
	}
}

func TestShouldSubmitHostUpsert_PerHostIndependent(t *testing.T) {
	p := newThrottlePoller()
	now := time.Now()

	guest := guestInfo("pve/lxc/100", "running")
	node := hosts.ConnectorHostInfo{
		HostInfo:    hosts.HostInfo{Name: "pve", MacAddress: "00:11:22:33:44:55"},
		HostType:    hosts.HostTypeHypervisor,
		ExternalID:  "pve",
		GuestStatus: "online",
	}

	if !p.shouldSubmitHostUpsert(guest, now) {
		t.Fatal("guest first submission must go through")
	}
	// A different host (the PVE node) is tracked independently: its first
	// submission still fires even though the guest was just submitted.
	if !p.shouldSubmitHostUpsert(node, now) {
		t.Fatal("a distinct host must have its own throttle state")
	}
	// Both unchanged on the next poll → both throttled.
	if p.shouldSubmitHostUpsert(guest, now.Add(pollInterval)) {
		t.Fatal("unchanged guest must be throttled")
	}
	if p.shouldSubmitHostUpsert(node, now.Add(pollInterval)) {
		t.Fatal("unchanged node must be throttled")
	}
}
