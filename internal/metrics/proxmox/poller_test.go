package proxmox

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/charmbracelet/log"
	"gorm.io/gorm"

	hosts "system-stats/internal/cluster/hosts"
	raftcluster "system-stats/internal/cluster/raft"
)

// stubRaftSvc is a raftcluster.Service whose leadership/enabled state is fixed,
// for exercising the poller's leader-gated handover (shouldPoll). Embedding
// DisabledService supplies every method we don't care about.
type stubRaftSvc struct {
	raftcluster.DisabledService
	enabled bool
	leader  bool
}

func (s stubRaftSvc) Enabled() bool  { return s.enabled }
func (s stubRaftSvc) IsLeader() bool { return s.leader }

// fakeHostRepo is a minimal hosts.Repository for findExisting tests: only the
// four lookup methods are implemented; any other call panics (none are made).
type fakeHostRepo struct {
	hosts.Repository
	byExternalID   map[string]*hosts.Host
	byMAC          map[string]*hosts.Host
	byUUID         map[string]*hosts.Host
	unlinkedByName map[string]*hosts.Host
}

func (f *fakeHostRepo) get(m map[string]*hosts.Host, k string) (*hosts.Host, error) {
	if h, ok := m[k]; ok {
		return h, nil
	}
	return nil, gorm.ErrRecordNotFound
}
func (f *fakeHostRepo) GetHostByExternalID(_ context.Context, id string) (*hosts.Host, error) {
	return f.get(f.byExternalID, id)
}
func (f *fakeHostRepo) GetHostByMacAddress(_ context.Context, mac string) (*hosts.Host, error) {
	return f.get(f.byMAC, mac)
}
func (f *fakeHostRepo) GetHostByHardwareUUID(_ context.Context, uuid string) (*hosts.Host, error) {
	return f.get(f.byUUID, uuid)
}
func (f *fakeHostRepo) GetUnlinkedAgentHostByName(_ context.Context, name string) (*hosts.Host, error) {
	return f.get(f.unlinkedByName, name)
}

// TestFindExistingSelfGuestDedup reproduces the dokploy duplicate: the node is
// its own Proxmox LXC, so the agent row (id=1, NIC MAC A, no external_id) and a
// connector-discovered row (id=41, NIC MAC B, external_id, SAME hardware UUID)
// coexist. findExisting must pick the local collector row as canonical (best)
// and mark the other same-machine row stale for removal.
func TestFindExistingSelfGuestDedup(t *testing.T) {
	const uuid = "032e02b4-0499-05c3-2206-ab0700080009"
	const extID = "proxmox:node/mika-pve@10.0.0.1:8006/mika-pve/lxc/103"
	agent := &hosts.Host{ID: hosts.LocalCollectorHostID, Name: "dokploy", MacAddress: "3e:d0:e2:ff:55:9b", Source: hosts.SourceAgent, HardwareUUID: uuid}
	dup := &hosts.Host{ID: 41, Name: "dokploy", MacAddress: "92:a5:fd:c4:0a:02", Source: hosts.MergeSource(hosts.SourceAgent, hosts.SourceConnector), ExternalID: extID, HardwareUUID: uuid}
	repo := &fakeHostRepo{
		byExternalID:   map[string]*hosts.Host{extID: dup},
		byMAC:          map[string]*hosts.Host{"92:a5:fd:c4:0a:02": dup},
		unlinkedByName: map[string]*hosts.Host{"dokploy": agent},
	}
	p := &Poller{deps: PollerDeps{Logger: log.New(io.Discard), HostRepo: repo}}

	best, stale := p.findExisting(context.Background(), extID, []string{"92:a5:fd:c4:0a:02"}, "", "dokploy")
	if best == nil || best.ID != hosts.LocalCollectorHostID {
		t.Fatalf("best must be the local collector row (id=%d), got %+v", hosts.LocalCollectorHostID, best)
	}
	if stale == nil || stale.ID != 41 {
		t.Fatalf("the same-machine connector dup (id=41) must be returned stale, got %+v", stale)
	}
}

// TestFindExistingNoDupKeepsSingleRow: once consolidated (one agent+connector
// row, no twin), findExisting returns it as best with no stale — stable, no churn.
func TestFindExistingNoDupKeepsSingleRow(t *testing.T) {
	const extID = "proxmox:node/mika-pve@10.0.0.1:8006/mika-pve/lxc/103"
	merged := &hosts.Host{ID: hosts.LocalCollectorHostID, Name: "dokploy", MacAddress: "3e:d0:e2:ff:55:9b", Source: hosts.MergeSource(hosts.SourceAgent, hosts.SourceConnector), ExternalID: extID, HardwareUUID: "u1"}
	repo := &fakeHostRepo{
		byExternalID: map[string]*hosts.Host{extID: merged},
		byMAC:        map[string]*hosts.Host{"3e:d0:e2:ff:55:9b": merged},
	}
	p := &Poller{deps: PollerDeps{Logger: log.New(io.Discard), HostRepo: repo}}

	best, stale := p.findExisting(context.Background(), extID, []string{"3e:d0:e2:ff:55:9b"}, "", "dokploy")
	if best == nil || best.ID != hosts.LocalCollectorHostID {
		t.Fatalf("best must be the single consolidated row, got %+v", best)
	}
	if stale != nil {
		t.Fatalf("no stale expected for a single consolidated row, got %+v", stale)
	}
}

func newThrottlePoller() *Poller {
	return &Poller{upsertState: map[string]hostUpsertState{}}
}

// TestEvictStale proves the per-cycle sweep prunes only this connector's
// no-longer-live cache keys, keeps live ones, and never touches another
// connector's entries — so the per-guest maps stay bounded to live topology.
func TestEvictStale(t *testing.T) {
	p := &Poller{
		nodeIdent:   map[string]cachedNodeIdentity{},
		guestCfg:    map[string]cachedGuestConfig{},
		netPrev:     map[string]netSample{},
		upsertState: map[string]hostUpsertState{},
	}
	const prefix = "proxmox:node/mika-pve@10.0.0.1:8006/"
	// Connector 1: live node + live guest, plus a stale guest (destroyed VMID).
	p.nodeIdent["1/mika-pve"] = cachedNodeIdentity{}
	p.guestCfg["1/mika-pve/lxc/103"] = cachedGuestConfig{}        // live
	p.guestCfg["1/mika-pve/qemu/199"] = cachedGuestConfig{}       // stale
	p.netPrev[prefix+"mika-pve/lxc/103"] = netSample{}            // live
	p.netPrev[prefix+"mika-pve/qemu/199"] = netSample{}           // stale
	p.upsertState[prefix+"mika-pve/lxc/103"] = hostUpsertState{}  // live
	p.upsertState[prefix+"mika-pve/qemu/199"] = hostUpsertState{} // stale
	// Connector 2's entries must survive connector 1's sweep untouched.
	p.guestCfg["2/other/lxc/103"] = cachedGuestConfig{}
	p.upsertState["proxmox:node/other@9.9.9.9:8006/other/lxc/103"] = hostUpsertState{}

	liveConnKeys := map[string]struct{}{
		"1/mika-pve":         {},
		"1/mika-pve/lxc/103": {},
	}
	liveExternalIDs := map[string]struct{}{
		prefix + "mika-pve":         {},
		prefix + "mika-pve/lxc/103": {},
	}
	p.evictStale(1, prefix, liveConnKeys, liveExternalIDs)

	for _, gone := range []string{"1/mika-pve/qemu/199"} {
		if _, ok := p.guestCfg[gone]; ok {
			t.Errorf("stale guestCfg %q should be evicted", gone)
		}
	}
	if _, ok := p.netPrev[prefix+"mika-pve/qemu/199"]; ok {
		t.Error("stale netPrev should be evicted")
	}
	if _, ok := p.upsertState[prefix+"mika-pve/qemu/199"]; ok {
		t.Error("stale upsertState should be evicted")
	}
	// Live entries kept.
	if _, ok := p.guestCfg["1/mika-pve/lxc/103"]; !ok {
		t.Error("live guestCfg must be kept")
	}
	if _, ok := p.nodeIdent["1/mika-pve"]; !ok {
		t.Error("live nodeIdent must be kept")
	}
	// Connector 2 untouched.
	if _, ok := p.guestCfg["2/other/lxc/103"]; !ok {
		t.Error("connector 2 guestCfg must not be touched by connector 1 sweep")
	}
	if _, ok := p.upsertState["proxmox:node/other@9.9.9.9:8006/other/lxc/103"]; !ok {
		t.Error("connector 2 upsertState must not be touched")
	}
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

// TestShouldPoll locks the in-cluster polling-node handover gate: exactly one
// writer per connector. A standalone node (no Raft, or Raft disabled) always
// polls; in a cluster only the current leader polls, so when leadership moves
// the new leader starts polling on its next tick and the old leader stops.
func TestShouldPoll(t *testing.T) {
	cases := []struct {
		name string
		svc  raftcluster.Service
		want bool
	}{
		{"standalone (no raft service)", nil, true},
		{"raft disabled", stubRaftSvc{enabled: false, leader: false}, true},
		{"clustered follower", stubRaftSvc{enabled: true, leader: false}, false},
		{"clustered leader", stubRaftSvc{enabled: true, leader: true}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := NewPoller(PollerDeps{RaftSvc: c.svc})
			if got := p.shouldPoll(); got != c.want {
				t.Fatalf("shouldPoll() = %v, want %v", got, c.want)
			}
		})
	}
}
