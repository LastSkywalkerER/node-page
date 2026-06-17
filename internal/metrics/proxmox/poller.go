package proxmox

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/log"
	"gorm.io/gorm"

	hosts "system-stats/internal/cluster/hosts"
	raftcluster "system-stats/internal/cluster/raft"
	cpu "system-stats/internal/metrics/cpu"
	disk "system-stats/internal/metrics/disk"
	memory "system-stats/internal/metrics/memory"
	network "system-stats/internal/metrics/network"
	connectors "system-stats/internal/platform/connectors"
)

const (
	// pollInterval is the live status/metrics cadence (one /cluster/resources
	// call per connector, plus one /nodes/{n}/status per online node).
	pollInterval = 10 * time.Second
	// identityTTL bounds the per-guest config / per-node network calls that
	// resolve MACs — topology changes slowly.
	identityTTL = 5 * time.Minute
	// hostUpsertHeartbeat is how often an UNCHANGED guest/node host row is still
	// pushed through Raft just to refresh last_seen so peers keep it online.
	// Proxmox guests change slowly, so we use a comfortable 60s — well below
	// HostOfflineThreshold — instead of firing a consensus round on every ~10s
	// poll. Mirrors internal/cluster/hosts.shouldSubmitUpsert.
	hostUpsertHeartbeat = 60 * time.Second
)

// PollerDeps bundles everything the Proxmox poller needs. When Raft is active
// all writes go through the replicated log (so every node converges); when
// standalone they fall back to direct repository writes + a local SSE publish.
type PollerDeps struct {
	Logger     *log.Logger
	Connectors connectors.Repository
	Cipher     *connectors.Cipher
	HostRepo   hosts.Repository
	// Detector supplies this machine's own "I am a Proxmox guest" hint
	// (incl. the VMID from the lxc cgroup) for the self-link flow.
	Detector *connectors.Detector

	Raft     *raftcluster.Replicator // nil-safe; Enabled() gates control-plane raft writes (host upsert/delete)
	RaftSvc  raftcluster.Service     // leader check; nil = standalone
	CPURepo  cpu.Repository
	MemRepo  memory.Repository
	DiskRepo disk.Repository
	NetRepo  network.Repository
	// Publish pushes a live SSE envelope to the local stream broker.
	Publish func([]byte)
	// BroadcastMetrics ships a guest's metric batch to cluster peers over the
	// best-effort metric stream (NOT the durable Raft log). nil = standalone:
	// metrics are only written locally. The poller always writes guest metrics
	// to the local repos + broker first, then broadcasts.
	BroadcastMetrics func(context.Context, raftcluster.MetricBatchPayload)
}

// Poller drives every enabled Proxmox connector: topology discovery, host
// linking by MAC, liveness and hypervisor/guest metrics. Exactly one node
// polls in a cluster — the Raft leader (a standalone node always polls).
type Poller struct {
	deps   PollerDeps
	syncCh chan struct{}

	mu        sync.Mutex
	nodeIdent map[string]cachedNodeIdentity // key: connectorID/nodeName
	guestCfg  map[string]cachedGuestConfig  // key: connectorID/node/kind/vmid
	netPrev   map[string]netSample          // key: externalID

	// upsertMu guards the per-host throttling state for replicated host upserts.
	upsertMu sync.Mutex
	// upsertState fingerprints the last host-upsert submitted through Raft for
	// each host (keyed by external_id), so an unchanged guest/node doesn't fire
	// a consensus round on every ~10s poll. See shouldSubmitHostUpsert.
	upsertState map[string]hostUpsertState
}

type hostUpsertState struct {
	hash       [sha256.Size]byte
	lastSubmit time.Time
}

type cachedNodeIdentity struct {
	mac, ip   string
	fetchedAt time.Time
}

type cachedGuestConfig struct {
	macs      []string
	ostype    string
	uuid      string // smbios1 UUID (QEMU only)
	ip        string // resolved IPv4 (LXC config/runtime, QEMU guest agent); "" if unknown
	fetchedAt time.Time
}

type netSample struct {
	in, out uint64
	at      time.Time
}

// NewPoller creates the Proxmox poller.
func NewPoller(deps PollerDeps) *Poller {
	return &Poller{
		deps:        deps,
		syncCh:      make(chan struct{}, 1),
		nodeIdent:   map[string]cachedNodeIdentity{},
		guestCfg:    map[string]cachedGuestConfig{},
		netPrev:     map[string]netSample{},
		upsertState: map[string]hostUpsertState{},
	}
}

// TriggerSync requests an immediate cycle (used by the admin "sync now" and
// right after connecting). Non-blocking.
func (p *Poller) TriggerSync() {
	select {
	case p.syncCh <- struct{}{}:
	default:
	}
}

// Run loops until ctx is cancelled.
func (p *Poller) Run(ctx context.Context) {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-p.syncCh:
		}
		p.cycle(ctx)
		// Self-link runs on EVERY node (not leader-gated): only this node
		// knows its own cgroup-derived VMID, and the claim replicates.
		p.selfLink(ctx)
	}
}

// selfLink claims this node's own guest row. MAC linking fails whenever the
// agent registered through a NIC that isn't in the guest's PVE config (Docker
// bridge / random veth — the dokploy-in-LXC case), leaving a connector-owned
// duplicate next to the agent row. The LXC cgroup leaks our VMID with high
// confidence, so when exactly one connector-fed guest matches "<kind>/<vmid>",
// this node attaches that topology to its own collector row and drops the
// duplicate. Idempotent: a linked row (external_id set) short-circuits.
func (p *Poller) selfLink(ctx context.Context) {
	if p.deps.Detector == nil {
		return
	}
	var hint *connectors.DiscoveredHint
	for _, h := range p.deps.Detector.Detect() {
		if h.Type == connectors.TypeProxmox && h.VMIDHint > 0 && h.GuestKind != "" {
			hh := h
			hint = &hh
			break
		}
	}
	if hint == nil {
		return
	}
	local, err := p.deps.HostRepo.GetHostByID(ctx, hosts.LocalCollectorHostID)
	if err != nil || local.ExternalID != "" || local.MacAddress == "" {
		return
	}
	suffix := fmt.Sprintf("/%s/%d", hint.GuestKind, hint.VMIDHint)
	twins, err := p.deps.HostRepo.FindConnectorOnlyHostsByExternalIDSuffix(ctx, suffix)
	if err != nil || len(twins) == 0 {
		return
	}
	if len(twins) > 1 {
		p.deps.Logger.Warn("proxmox: self-link ambiguous — several connectors expose this VMID; skipping",
			"vmid", hint.VMIDHint, "kind", hint.GuestKind, "matches", len(twins))
		return
	}
	twin := twins[0]
	p.deps.Logger.Info("proxmox: linking this node to its hypervisor guest",
		"external_id", twin.ExternalID, "replacing_host_id", twin.ID)
	// Drop the duplicate first, then attach its topology to our agent row
	// (UpsertConnectorHost applies a column-limited topology update there).
	p.removeHost(ctx, &twin)
	p.upsertHost(ctx, hosts.ConnectorHostInfo{
		HostInfo: hosts.HostInfo{
			Name:       local.Name,
			MacAddress: local.MacAddress,
		},
		HostType:    twin.HostType,
		ParentMAC:   twin.ParentMAC,
		ExternalID:  twin.ExternalID,
		GuestStatus: twin.GuestStatus,
	})
}

// shouldPoll: the Raft leader (or a standalone node) owns polling so the
// cluster sees exactly one writer per connector.
func (p *Poller) shouldPoll() bool {
	if p.deps.RaftSvc == nil || !p.deps.RaftSvc.Enabled() {
		return true
	}
	return p.deps.RaftSvc.IsLeader()
}

func (p *Poller) cycle(ctx context.Context) {
	if !p.shouldPoll() {
		return
	}
	listCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	conns, err := p.deps.Connectors.List(listCtx)
	cancel()
	if err != nil {
		p.deps.Logger.Warn("proxmox: list connectors", "error", err)
		return
	}
	// Live key sets for the cache eviction below: which connectors are still
	// active this cycle. connID-keyed maps (nodeIdent/guestCfg) match on
	// "<id>/"; external-id-keyed maps (netPrev/upsertState) match on the
	// connector's external-id prefix.
	liveConnPrefixes := make(map[string]struct{})
	livePrefixes := make(map[string]struct{})
	for _, conn := range conns {
		if conn.Type != connectors.TypeProxmox {
			continue
		}
		if !conn.Enabled {
			if conn.Status != connectors.StatusDisabled {
				_ = p.deps.Connectors.UpdateLocalStatus(ctx, conn.ID, connectors.StatusDisabled, "", time.Time{})
			}
			continue
		}
		liveConnPrefixes[fmt.Sprintf("%d/", conn.ID)] = struct{}{}
		livePrefixes[connectors.ExternalIDPrefix(conn.Type, conn.Fingerprint)] = struct{}{}
		connCtx, connCancel := context.WithTimeout(ctx, 45*time.Second)
		p.syncConnector(connCtx, conn)
		connCancel()
	}

	// Drop caches for connectors that are no longer active (disabled or deleted):
	// per-connector evictStale only runs for connectors that sync, so without this
	// a removed connector's identity/guest/network caches would linger for the
	// whole process lifetime.
	p.evictDisconnected(liveConnPrefixes, livePrefixes)
}

// evictDisconnected removes every cache entry whose owning connector is not in
// the live sets. An enabled-but-temporarily-unreachable connector stays in the
// live sets (it failed to sync, but it's still listed+enabled), so only truly
// disabled/deleted connectors are evicted.
func (p *Poller) evictDisconnected(liveConnPrefixes, livePrefixes map[string]struct{}) {
	hasAny := func(key string, set map[string]struct{}) bool {
		for pfx := range set {
			if pfx != "" && strings.HasPrefix(key, pfx) {
				return true
			}
		}
		return false
	}
	p.mu.Lock()
	for k := range p.nodeIdent {
		if !hasAny(k, liveConnPrefixes) {
			delete(p.nodeIdent, k)
		}
	}
	for k := range p.guestCfg {
		if !hasAny(k, liveConnPrefixes) {
			delete(p.guestCfg, k)
		}
	}
	for k := range p.netPrev {
		if !hasAny(k, livePrefixes) {
			delete(p.netPrev, k)
		}
	}
	p.mu.Unlock()

	p.upsertMu.Lock()
	for k := range p.upsertState {
		if !hasAny(k, livePrefixes) {
			delete(p.upsertState, k)
		}
	}
	p.upsertMu.Unlock()
}

func (p *Poller) syncConnector(ctx context.Context, conn connectors.Connector) {
	secret, err := p.deps.Cipher.Decrypt(conn.SecretEnc)
	if err != nil {
		p.setStatus(ctx, conn.ID, connectors.StatusAuthFailed, "cannot decrypt secret (JWT secret changed?): "+err.Error())
		return
	}
	client, err := NewClient(conn.Endpoint, conn.TokenID, secret, conn.SkipTLSVerify)
	if err != nil {
		p.setStatus(ctx, conn.ID, connectors.StatusUnreachable, err.Error())
		return
	}
	// A client is built per cycle; release its idle connections at the end so
	// the discarded transport doesn't leak parked keep-alive goroutines.
	defer client.Close()
	resources, err := client.ClusterResources(ctx)
	if err != nil {
		if _, ok := err.(*AuthError); ok {
			p.setStatus(ctx, conn.ID, connectors.StatusAuthFailed, wrapAuth(err, conn.TokenID).Error())
		} else {
			p.setStatus(ctx, conn.ID, connectors.StatusUnreachable, err.Error())
		}
		return
	}

	prefix := connectors.ExternalIDPrefix(conn.Type, conn.Fingerprint)
	nodeMACs := map[string]string{}

	// Live key sets for this cycle, used to evict stale cache entries below.
	// liveConnKeys holds nodeIdent (connID/node) + guestCfg (connID/node/kind/vmid)
	// keys; liveExternalIDs holds the node/guest external_ids that key netPrev and
	// upsertState. They mirror the exact formats used at the map-write sites.
	liveConnKeys := make(map[string]struct{}, len(resources))
	liveExternalIDs := make(map[string]struct{}, len(resources))

	// Hypervisor nodes first so guests can reference their parent MAC.
	for _, r := range resources {
		if r.Type != "node" {
			continue
		}
		liveConnKeys[fmt.Sprintf("%d/%s", conn.ID, r.Node)] = struct{}{}
		liveExternalIDs[prefix+r.Node] = struct{}{}
		mac := p.syncNode(ctx, client, conn, prefix, r)
		if mac != "" {
			nodeMACs[r.Node] = mac
		}
	}
	for _, r := range resources {
		if (r.Type != "qemu" && r.Type != "lxc") || r.Template == 1 {
			continue
		}
		liveConnKeys[fmt.Sprintf("%d/%s/%s/%d", conn.ID, r.Node, r.Type, r.VMID)] = struct{}{}
		liveExternalIDs[fmt.Sprintf("%s%s/%s/%d", prefix, r.Node, r.Type, r.VMID)] = struct{}{}
		p.syncGuest(ctx, client, conn, prefix, r, nodeMACs[r.Node])
	}

	// Prune cache entries for nodes/guests that no longer exist on this connector
	// (destroyed/recreated VMIDs, migrations) so the per-guest maps stay bounded
	// to the live topology instead of growing for the process lifetime. Only runs
	// after a SUCCESSFUL resource fetch (a failed fetch returns early above), so a
	// transient outage never wipes still-valid state.
	p.evictStale(conn.ID, prefix, liveConnKeys, liveExternalIDs)

	// Remove host ROWS for guests/nodes that no longer exist on the connector
	// (e.g. a VM/LXC deleted in Proxmox) — otherwise they linger forever as
	// "stopped". Same safety as evictStale: only after a successful resource
	// fetch, so a transient outage never deletes still-valid rows.
	p.pruneVanished(ctx, prefix, liveExternalIDs)

	_ = p.deps.Connectors.UpdateLocalStatus(ctx, conn.ID, connectors.StatusOK, "", time.Now())
}

// pruneVanished deletes connector-owned host rows under prefix whose external_id
// is absent from this cycle's live set — guests/nodes removed at the source.
// Pure connector rows only (agent / agent+connector rows are left to the agent);
// the local collector row is excluded by the repository query.
func (p *Poller) pruneVanished(ctx context.Context, prefix string, liveExternalIDs map[string]struct{}) {
	existing, err := p.deps.HostRepo.FindConnectorOnlyHostsByExternalIDPrefix(ctx, prefix)
	if err != nil {
		p.deps.Logger.Warn("proxmox: list connector hosts for prune", "error", err)
		return
	}
	for i := range existing {
		h := existing[i]
		if h.ExternalID == "" {
			continue
		}
		if _, live := liveExternalIDs[h.ExternalID]; live {
			continue
		}
		p.deps.Logger.Info("proxmox: removing host no longer present on the connector",
			"external_id", h.ExternalID, "name", h.Name, "host_id", h.ID)
		p.removeHost(ctx, &h)
	}
}

// evictStale removes cached identity/network/upsert state for one connector's
// nodes/guests that are not in the live set for the current cycle. Keys are
// scoped to this connector (connID/ prefix for the mu-guarded maps, external_id
// prefix for the upsert map) so a connector's sweep never touches another's
// entries. Mirrors the docker collector's per-cycle eviction.
func (p *Poller) evictStale(connID uint, prefix string, liveConnKeys, liveExternalIDs map[string]struct{}) {
	connPrefix := fmt.Sprintf("%d/", connID)

	p.mu.Lock()
	for k := range p.nodeIdent {
		if strings.HasPrefix(k, connPrefix) {
			if _, ok := liveConnKeys[k]; !ok {
				delete(p.nodeIdent, k)
			}
		}
	}
	for k := range p.guestCfg {
		if strings.HasPrefix(k, connPrefix) {
			if _, ok := liveConnKeys[k]; !ok {
				delete(p.guestCfg, k)
			}
		}
	}
	for k := range p.netPrev {
		if strings.HasPrefix(k, prefix) {
			if _, ok := liveExternalIDs[k]; !ok {
				delete(p.netPrev, k)
			}
		}
	}
	p.mu.Unlock()

	p.upsertMu.Lock()
	for k := range p.upsertState {
		if strings.HasPrefix(k, prefix) {
			if _, ok := liveExternalIDs[k]; !ok {
				delete(p.upsertState, k)
			}
		}
	}
	p.upsertMu.Unlock()
}

func (p *Poller) setStatus(ctx context.Context, id uint, status, msg string) {
	p.deps.Logger.Warn("proxmox: connector sync failed", "connector_id", id, "status", status, "error", msg)
	_ = p.deps.Connectors.UpdateLocalStatus(ctx, id, status, msg, time.Time{})
}

// syncNode upserts the hypervisor host row + its metrics; returns its MAC.
func (p *Poller) syncNode(ctx context.Context, client *Client, conn connectors.Connector, prefix string, r Resource) string {
	externalID := prefix + r.Node
	mac, ip := p.nodeIdentity(ctx, client, conn.ID, r.Node, externalID)

	info := hosts.ConnectorHostInfo{
		HostInfo: hosts.HostInfo{
			Name:       r.Node,
			MacAddress: mac,
			IPv4:       ip,
			OS:         "linux",
			Platform:   "proxmox",
		},
		HostType:    hosts.HostTypeHypervisor,
		ExternalID:  externalID,
		GuestStatus: r.Status, // online | offline
	}

	var status *NodeStatus
	if r.Status == "online" {
		if st, err := client.NodeStatus(ctx, r.Node); err == nil {
			status = st
			info.PlatformVersion = strings.TrimSpace(st.PVEVersion)
			info.KernelVersion = strings.TrimSpace(st.KVersion)
			if st.Uptime > 0 {
				info.BootTime = time.Now().Unix() - st.Uptime
			}
		} else if r.Uptime > 0 {
			info.BootTime = time.Now().Unix() - r.Uptime
		}
	}

	host := p.upsertHost(ctx, info)
	if host == nil || r.Status != "online" {
		return mac
	}
	// Feed metrics only while the connector owns the row — an agent installed
	// directly on the PVE node provides the real thing.
	if hosts.MergeSource(host.Source, hosts.SourceConnector) != hosts.SourceConnector {
		return mac
	}

	cpuM := &cpu.CPUMetric{
		UsagePercent: r.CPU * 100,
		Cores:        int(r.MaxCPU),
	}
	memM := &memory.MemoryMetric{
		Total:        r.MaxMem,
		Used:         r.Mem,
		UsagePercent: pct(r.Mem, r.MaxMem),
	}
	diskM := &disk.DiskMetric{
		Total:        r.MaxDisk,
		Used:         r.Disk,
		UsagePercent: pct(r.Disk, r.MaxDisk),
	}
	if status != nil {
		load := status.LoadAvgFloats()
		cpuM.LoadAvg1, cpuM.LoadAvg5, cpuM.LoadAvg15 = load[0], load[1], load[2]
		cpuM.ModelName = status.CPUInfo.Model
		if status.CPUInfo.CPUs > 0 {
			cpuM.Cores = status.CPUInfo.CPUs
		}
		if status.Memory.Total > 0 {
			memM.Total = status.Memory.Total
			memM.Used = status.Memory.Used
			memM.Free = status.Memory.Free
			memM.UsagePercent = pct(status.Memory.Used, status.Memory.Total)
		}
		if status.RootFS.Total > 0 {
			diskM.Total = status.RootFS.Total
			diskM.Used = status.RootFS.Used
			diskM.Free = status.RootFS.Free
			diskM.UsagePercent = pct(status.RootFS.Used, status.RootFS.Total)
		}
	}
	memM.Available = memM.Total - memM.Used
	if diskM.Free == 0 && diskM.Total >= diskM.Used {
		diskM.Free = diskM.Total - diskM.Used
	}
	p.submitMetrics(ctx, host, cpuM, memM, diskM, p.nodeNetwork(ctx, client, r.Node))
	return mac
}

// nodeNetwork synthesizes the hypervisor's network metric from rrddata — the
// only node-level traffic the PVE API exposes (netin/netout rates in B/s).
func (p *Poller) nodeNetwork(ctx context.Context, client *Client, node string) *network.NetworkMetric {
	pts, err := client.NodeRRDData(ctx, node)
	if err != nil {
		p.deps.Logger.Debug("proxmox: node rrddata unavailable", "node", node, "error", err)
		return nil
	}
	for i := len(pts) - 1; i >= 0; i-- {
		if pts[i].NetIn == nil || pts[i].NetOut == nil {
			continue
		}
		return &network.NetworkMetric{Interfaces: []network.NetworkInterface{{
			Name:          "node",
			IsPrimary:     true,
			SpeedKbpsRecv: *pts[i].NetIn * 8 / 1024,
			SpeedKbpsSent: *pts[i].NetOut * 8 / 1024,
		}}}
	}
	return nil
}

func (p *Poller) syncGuest(ctx context.Context, client *Client, conn connectors.Connector, prefix string, r Resource, parentMAC string) {
	externalID := fmt.Sprintf("%s%s/%s/%d", prefix, r.Node, r.Type, r.VMID)
	cfg := p.guestIdentity(ctx, client, conn.ID, r)

	mac := externalID // synthetic fallback for NIC-less guests
	if len(cfg.macs) > 0 {
		mac = cfg.macs[0]
	}
	// Linking: if ANY of the guest's NIC MACs — or, for VMs, its SMBIOS UUID,
	// or (weakest) an agent whose hostname equals the guest name — matches a
	// registered host, that row becomes the guest — no duplicate.
	existing, stale := p.findExisting(ctx, externalID, cfg.macs, cfg.uuid, r.Name)
	if stale != nil {
		// An agent row appeared for a guest we had been tracking through a
		// different MAC — drop our redundant connector-only row.
		p.removeHost(ctx, stale)
	}
	if existing != nil {
		mac = existing.MacAddress
	}

	hostType := hosts.HostTypeVM
	if r.Type == "lxc" {
		hostType = hosts.HostTypeLXC
	}
	osName, platform, family := OSTypeInfo(cfg.ostype)
	info := hosts.ConnectorHostInfo{
		HostInfo: hosts.HostInfo{
			Name:           r.Name,
			MacAddress:     mac,
			IPv4:           cfg.ip,
			OS:             osName,
			Platform:       platform,
			PlatformFamily: family,
		},
		HostType:    hostType,
		ParentMAC:   parentMAC,
		ExternalID:  externalID,
		GuestStatus: r.Status,
	}
	if r.Status == "running" && r.Uptime > 0 {
		info.BootTime = time.Now().Unix() - r.Uptime
	}

	agentOwned := existing != nil &&
		hosts.MergeSource(existing.Source, hosts.SourceConnector) != hosts.SourceConnector
	if agentOwned && !topologyChanged(existing, info) {
		// Agent rows only need a (replicated) write when topology/state moved;
		// their liveness and metrics come from the agent itself.
		return
	}

	host := p.upsertHost(ctx, info)
	if host == nil || agentOwned || r.Status != "running" {
		return
	}

	cpuM := &cpu.CPUMetric{UsagePercent: r.CPU * 100, Cores: int(r.MaxCPU)}
	memM := &memory.MemoryMetric{
		Total:        r.MaxMem,
		Used:         r.Mem,
		Available:    r.MaxMem - r.Mem,
		UsagePercent: pct(r.Mem, r.MaxMem),
	}
	var diskM *disk.DiskMetric
	if r.MaxDisk > 0 && r.Disk > 0 { // qemu reports 0 used without the guest agent
		diskM = &disk.DiskMetric{
			Total:        r.MaxDisk,
			Used:         r.Disk,
			Free:         r.MaxDisk - r.Disk,
			UsagePercent: pct(r.Disk, r.MaxDisk),
		}
	}
	p.submitMetrics(ctx, host, cpuM, memM, diskM, p.guestNetwork(externalID, r))
}

// findExisting resolves the guest to an already-known host row, strongest key
// first: the external_id column, NIC MACs (or the synthetic external-id MAC),
// the SMBIOS UUID (a VM agent's product_uuid equals the guest's smbios1 UUID
// even when its registered MAC is a Docker-bridge one the PVE config has never
// seen), and finally an unlinked agent whose hostname equals the guest name —
// the only handle for an LXC agent behind a Docker bridge. When both an agent
// row and a connector-only row match, the agent row wins and the connector
// row is returned as stale for cleanup.
func (p *Poller) findExisting(ctx context.Context, externalID string, macs []string, uuid, guestName string) (best, stale *hosts.Host) {
	var matches []*hosts.Host
	add := func(h *hosts.Host) {
		for _, m := range matches {
			if m.ID == h.ID {
				return
			}
		}
		matches = append(matches, h)
	}
	// The external_id column first: a self-linked agent row carries the guest
	// identity while its registered MAC is absent from the PVE config.
	if h, err := p.deps.HostRepo.GetHostByExternalID(ctx, externalID); err == nil {
		add(h)
	}
	for _, key := range append([]string{externalID}, macs...) {
		if h, err := p.deps.HostRepo.GetHostByMacAddress(ctx, key); err == nil {
			add(h)
		}
	}
	if uuid != "" {
		if h, err := p.deps.HostRepo.GetHostByHardwareUUID(ctx, uuid); err == nil {
			add(h)
		}
	}
	if guestName != "" {
		if h, err := p.deps.HostRepo.GetUnlinkedAgentHostByName(ctx, guestName); err == nil {
			p.deps.Logger.Info("proxmox: linking guest to agent host by matching name",
				"name", guestName, "host_id", h.ID)
			add(h)
		}
	}
	// The local collector's own row (LocalCollectorHostID) always wins when it
	// matched — this is the self-guest case (the node is an LXC/VM of its own
	// Proxmox). It must never be orphaned or deleted, so it is the canonical row
	// the connector topology attaches to.
	for _, h := range matches {
		if h.ID == hosts.LocalCollectorHostID {
			best = h
			break
		}
	}
	if best == nil {
		for _, h := range matches {
			if hosts.MergeSource(h.Source, hosts.SourceConnector) != hosts.SourceConnector {
				best = h // agent-maintained row wins over a pure connector row
				break
			}
		}
	}
	if best == nil {
		if len(matches) > 0 {
			return matches[0], nil
		}
		return nil, nil
	}
	// A second matched row for the SAME machine is a duplicate to clean up:
	//   - a pure connector row (the original "agent appeared via a different MAC"
	//     case), or
	//   - any other row sharing best's non-empty hardware UUID — the self-guest
	//     dup, where the agent row (id=1, NIC MAC A) and a connector-discovered
	//     row (NIC MAC B from the PVE config) coexist for one physical machine.
	// Never return the local collector row as stale (it is canonical).
	for _, h := range matches {
		if h.ID == best.ID || h.ID == hosts.LocalCollectorHostID {
			continue
		}
		sameMachine := best.HardwareUUID != "" && h.HardwareUUID == best.HardwareUUID
		if h.Source == hosts.SourceConnector || sameMachine {
			return best, h
		}
	}
	return best, nil
}

// removeHost cascades a host removal (replicated when Raft is on).
func (p *Poller) removeHost(ctx context.Context, h *hosts.Host) {
	p.deps.Logger.Info("proxmox: removing redundant connector host row", "host_id", h.ID, "name", h.Name)
	if p.deps.Raft != nil && p.deps.Raft.Enabled() {
		if err := p.deps.Raft.SubmitHostDelete(ctx, h.MacAddress); err != nil {
			p.deps.Logger.Warn("proxmox: replicate host delete", "host", h.Name, "error", err)
		}
		return
	}
	if err := p.deps.HostRepo.DeleteHostCascade(ctx, h.ID); err != nil {
		p.deps.Logger.Warn("proxmox: delete host", "host", h.Name, "error", err)
	}
}

func topologyChanged(existing *hosts.Host, info hosts.ConnectorHostInfo) bool {
	return existing.HostType != info.HostType ||
		!strings.EqualFold(existing.ParentMAC, info.ParentMAC) ||
		existing.ExternalID != info.ExternalID ||
		existing.GuestStatus != info.GuestStatus
}

// guestNetwork synthesizes one pseudo-interface from the cumulative netin/
// netout counters, with speeds derived from the previous sample.
func (p *Poller) guestNetwork(externalID string, r Resource) *network.NetworkMetric {
	if r.NetIn == 0 && r.NetOut == 0 {
		return nil
	}
	now := time.Now()
	iface := network.NetworkInterface{
		Name:      "net0",
		BytesRecv: r.NetIn,
		BytesSent: r.NetOut,
		IsPrimary: true,
	}
	p.mu.Lock()
	if prev, ok := p.netPrev[externalID]; ok {
		dt := now.Sub(prev.at).Seconds()
		if dt > 0 && r.NetIn >= prev.in && r.NetOut >= prev.out {
			iface.SpeedKbpsRecv = float64(r.NetIn-prev.in) / dt / 1024 * 8
			iface.SpeedKbpsSent = float64(r.NetOut-prev.out) / dt / 1024 * 8
		}
	}
	p.netPrev[externalID] = netSample{in: r.NetIn, out: r.NetOut, at: now}
	p.mu.Unlock()
	return &network.NetworkMetric{Interfaces: []network.NetworkInterface{iface}}
}

// nodeIdentity resolves (and caches) a PVE node's MAC + IP from its network
// config. Falls back to the synthetic external id when no NIC is readable.
func (p *Poller) nodeIdentity(ctx context.Context, client *Client, connID uint, node, externalID string) (string, string) {
	key := fmt.Sprintf("%d/%s", connID, node)
	p.mu.Lock()
	cached, ok := p.nodeIdent[key]
	p.mu.Unlock()
	if ok && time.Since(cached.fetchedAt) < identityTTL {
		return cached.mac, cached.ip
	}

	mac, ip := externalID, ""
	if ifaces, err := client.NodeNetwork(ctx, node); err == nil {
		// MAC and IP are resolved INDEPENDENTLY: on a PVE node the management IP
		// lives on a bridge (vmbr0) while a usable MAC may only be on the physical
		// NIC — coupling them to one interface drops the IP when the highest-scored
		// MAC-bearing iface has no address (or vice-versa).
		if m := pickNodeMAC(ifaces); m != "" {
			mac = m
		}
		ip = pickNodeIPv4(ifaces)
	} else {
		p.deps.Logger.Debug("proxmox: node network unavailable", "node", node, "error", err)
	}

	p.mu.Lock()
	p.nodeIdent[key] = cachedNodeIdentity{mac: mac, ip: ip, fetchedAt: time.Now()}
	p.mu.Unlock()
	return mac, ip
}

// pickNodeMAC returns the best MAC among interfaces that carry one (gateway >
// address > eth), or "" when none do.
func pickNodeMAC(ifaces []NodeNetIface) string {
	best := -1
	for i := range ifaces {
		if normalizeMAC(ifaces[i].HWAddr) == "" {
			continue
		}
		if best == -1 || ifaceScore(ifaces[i]) > ifaceScore(ifaces[best]) {
			best = i
		}
	}
	if best >= 0 {
		return normalizeMAC(ifaces[best].HWAddr)
	}
	return ""
}

// pickNodeIPv4 returns the best IPv4 among interfaces that carry one (gateway >
// address > eth), independent of whether that interface exposes a MAC.
func pickNodeIPv4(ifaces []NodeNetIface) string {
	best := -1
	for i := range ifaces {
		if ifaceIPv4(ifaces[i]) == "" {
			continue
		}
		if best == -1 || ifaceScore(ifaces[i]) > ifaceScore(ifaces[best]) {
			best = i
		}
	}
	if best >= 0 {
		return ifaceIPv4(ifaces[best])
	}
	return ""
}

// ifaceIPv4 extracts a usable IPv4 from an interface's address or cidr field
// (newer PVE returns the management IP only in cidr). "" when neither is a v4.
func ifaceIPv4(nf NodeNetIface) string {
	for _, v := range []string{nf.Address, nf.CIDR} {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		host := strings.SplitN(v, "/", 2)[0]
		if ip := net.ParseIP(host); ip != nil && ip.To4() != nil {
			return host
		}
	}
	return ""
}

func ifaceScore(nf NodeNetIface) int {
	switch {
	case nf.Gateway != "":
		return 3
	case ifaceIPv4(nf) != "":
		return 2
	case nf.Type == "eth":
		return 1
	}
	return 0
}

// guestIdentity resolves (and caches) a guest's NIC MACs + ostype.
func (p *Poller) guestIdentity(ctx context.Context, client *Client, connID uint, r Resource) cachedGuestConfig {
	key := fmt.Sprintf("%d/%s/%s/%d", connID, r.Node, r.Type, r.VMID)
	p.mu.Lock()
	cached, ok := p.guestCfg[key]
	p.mu.Unlock()
	if ok && time.Since(cached.fetchedAt) < identityTTL {
		return cached
	}

	out := cachedGuestConfig{fetchedAt: time.Now()}
	if cfg, err := client.GuestConfig(ctx, r.Node, r.Type, r.VMID); err == nil {
		out.macs = ConfigMACs(cfg)
		out.uuid = SMBIOSUUID(cfg)
		if v, ok := cfg["ostype"].(string); ok {
			out.ostype = v
		}
		out.ip = ConfigIPv4(cfg) // LXC static ip=...; "" for dhcp / QEMU
	} else {
		p.deps.Logger.Debug("proxmox: guest config unavailable", "node", r.Node, "vmid", r.VMID, "error", err)
		out.fetchedAt = time.Now().Add(identityTTL - 30*time.Second) // retry soon
	}

	// Resolve a runtime IP the static config didn't carry (DHCP LXC / QEMU),
	// best-effort and only while running. QEMU needs the qemu-guest-agent.
	if out.ip == "" && r.Status == "running" {
		out.ip = p.guestRuntimeIPv4(ctx, client, r)
	}

	p.mu.Lock()
	p.guestCfg[key] = out
	p.mu.Unlock()
	return out
}

// guestRuntimeIPv4 best-effort resolves a running guest's IPv4 from PVE's
// runtime endpoints: the LXC interfaces list, or the QEMU guest agent (no agent
// → error → ""). Returns "" on any failure. Cached by the guestIdentity TTL.
func (p *Poller) guestRuntimeIPv4(ctx context.Context, client *Client, r Resource) string {
	switch r.Type {
	case "lxc":
		ifaces, err := client.LXCInterfaces(ctx, r.Node, r.VMID)
		if err != nil {
			p.deps.Logger.Debug("proxmox: lxc interfaces unavailable", "node", r.Node, "vmid", r.VMID, "error", err)
			return ""
		}
		return firstLXCIPv4(ifaces)
	case "qemu":
		ips, err := client.GuestAgentInterfaces(ctx, r.Node, r.VMID)
		if err != nil {
			p.deps.Logger.Debug("proxmox: qemu guest agent unavailable", "node", r.Node, "vmid", r.VMID, "error", err)
			return ""
		}
		if len(ips) > 0 {
			return ips[0]
		}
	}
	return ""
}

// upsertHost routes the host write through Raft (replicated) or directly.
// Returns the local row (resolved post-apply) or nil on failure.
//
// The Raft path is THROTTLED per host (see shouldSubmitHostUpsert): a guest/node
// whose identity, topology and status haven't changed and was submitted within
// the last hostUpsertHeartbeat is skipped — no consensus round — while the row
// is still resolved and returned so metrics keep flowing and last_seen stays
// fresh via the periodic heartbeat. Status transitions (guest_status) are part
// of the fingerprint, so a stop/start replicates on the very next poll.
func (p *Poller) upsertHost(ctx context.Context, info hosts.ConnectorHostInfo) *hosts.Host {
	if p.deps.Raft != nil && p.deps.Raft.Enabled() {
		if p.shouldSubmitHostUpsert(info, time.Now()) {
			if err := p.deps.Raft.SubmitConnectorHostUpsert(ctx, info); err != nil {
				p.deps.Logger.Warn("proxmox: replicate host upsert", "host", info.Name, "error", err)
				return nil
			}
		}
	} else {
		if _, err := p.deps.HostRepo.UpsertConnectorHost(ctx, info); err != nil {
			p.deps.Logger.Warn("proxmox: upsert host", "host", info.Name, "error", err)
			return nil
		}
	}
	host, err := p.deps.HostRepo.GetHostByMacAddress(ctx, info.MacAddress)
	if err != nil {
		if err != gorm.ErrRecordNotFound {
			p.deps.Logger.Warn("proxmox: resolve host after upsert", "host", info.Name, "error", err)
		}
		return nil
	}
	return host
}

// connectorHostFingerprint hashes every identity/topology/status field of a
// ConnectorHostInfo — everything a peer cares about, excluding last_seen (which
// the applier stamps). guest_status is included so an online↔stopped transition
// changes the fingerprint and replicates on the next poll. Mirrors
// internal/cluster/hosts.hostInfoFingerprint.
func connectorHostFingerprint(info hosts.ConnectorHostInfo) [sha256.Size]byte {
	// A NUL field separator keeps distinct field boundaries unambiguous.
	s := strings.Join([]string{
		info.Name,
		info.MacAddress,
		info.IPv4,
		info.OS,
		info.Platform,
		info.PlatformFamily,
		info.PlatformVersion,
		info.KernelVersion,
		fmt.Sprintf("%d", info.BootTime),
		info.HostType,
		info.ParentMAC,
		info.ExternalID,
		info.GuestStatus,
	}, "\x00")
	return sha256.Sum256([]byte(s))
}

// shouldSubmitHostUpsert reports whether the replicated host upsert for this
// guest/node should issue a Raft consensus round now, and records the decision.
// It submits when the record changed since the last submission for this host,
// or when at least hostUpsertHeartbeat has elapsed (a liveness refresh so peers
// keep the guest online). Keyed by external_id (cluster-stable per guest/node);
// falls back to MAC when a row carries no external id. Mutex-protected.
func (p *Poller) shouldSubmitHostUpsert(info hosts.ConnectorHostInfo, now time.Time) bool {
	key := info.ExternalID
	if key == "" {
		key = info.MacAddress
	}
	fp := connectorHostFingerprint(info)

	p.upsertMu.Lock()
	defer p.upsertMu.Unlock()

	prev, seen := p.upsertState[key]
	changed := !seen || fp != prev.hash
	elapsed := now.Sub(prev.lastSubmit) >= hostUpsertHeartbeat
	if seen && !changed && !elapsed {
		return false
	}
	p.upsertState[key] = hostUpsertState{hash: fp, lastSubmit: now}
	return true
}

// submitMetrics replicates a metric batch (Raft) or saves + publishes locally.
func (p *Poller) submitMetrics(ctx context.Context, host *hosts.Host, cpuM *cpu.CPUMetric, memM *memory.MemoryMetric, diskM *disk.DiskMetric, netM *network.NetworkMetric) {
	ts := time.Now().UTC()

	marshal := func(v any) json.RawMessage {
		if v == nil {
			return nil
		}
		b, err := json.Marshal(v)
		if err != nil {
			return nil
		}
		return b
	}

	// Persist the guest's metrics LOCALLY (on the polling node) + push to its SSE
	// broker. Metrics no longer ride the durable Raft log; the cluster fanout
	// below replicates them best-effort instead.
	env := map[string]any{"collecting_host_id": host.ID, "timestamp": ts}
	if cpuM != nil {
		if err := p.deps.CPURepo.SaveCurrentMetricAt(ctx, *cpuM, host.ID, ts); err == nil {
			env["cpu"] = cpuM
		}
	}
	if memM != nil {
		if err := p.deps.MemRepo.SaveCurrentMetricAt(ctx, *memM, host.ID, ts); err == nil {
			env["memory"] = memM
		}
	}
	if diskM != nil {
		if err := p.deps.DiskRepo.SaveCurrentMetricAt(ctx, *diskM, host.ID, ts); err == nil {
			env["disk"] = diskM
		}
	}
	if netM != nil {
		if err := p.deps.NetRepo.SaveCurrentMetricAt(ctx, *netM, host.ID, ts); err == nil {
			env["network"] = netM
		}
	}
	if p.deps.Publish != nil {
		if b, err := json.Marshal(env); err == nil {
			p.deps.Publish(b)
		}
	}

	// Best-effort cluster fanout: ship the guest batch to peers (and the
	// cross-cluster hub) over the metric stream. nil on a standalone node.
	if p.deps.BroadcastMetrics != nil {
		batch := raftcluster.MetricBatchPayload{HostMAC: host.MacAddress, HostName: host.Name, Timestamp: ts}
		if cpuM != nil {
			batch.CPU = marshal(cpuM)
		}
		if memM != nil {
			batch.Memory = marshal(memM)
		}
		if diskM != nil {
			batch.Disk = marshal(diskM)
		}
		if netM != nil {
			batch.Network = marshal(netM)
		}
		p.deps.BroadcastMetrics(ctx, batch)
	}
}

func pct(used, total uint64) float64 {
	if total == 0 {
		return 0
	}
	return float64(used) / float64(total) * 100
}
