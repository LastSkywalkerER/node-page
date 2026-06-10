package proxmox

import (
	"context"
	"encoding/json"
	"fmt"
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
)

// PollerDeps bundles everything the Proxmox poller needs. When Raft is active
// all writes go through the replicated log (so every node converges); when
// standalone they fall back to direct repository writes + a local SSE publish.
type PollerDeps struct {
	Logger     *log.Logger
	Connectors connectors.Repository
	Cipher     *connectors.Cipher
	HostRepo   hosts.Repository

	Raft     *raftcluster.Replicator // nil-safe; Enabled() gates the raft path
	RaftSvc  raftcluster.Service     // leader check; nil = standalone
	CPURepo  cpu.Repository
	MemRepo  memory.Repository
	DiskRepo disk.Repository
	NetRepo  network.Repository
	// Publish pushes a live SSE envelope to the local stream broker
	// (standalone path only — the Raft applier publishes on every node).
	Publish func([]byte)
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
}

type cachedNodeIdentity struct {
	mac, ip   string
	fetchedAt time.Time
}

type cachedGuestConfig struct {
	macs      []string
	ostype    string
	fetchedAt time.Time
}

type netSample struct {
	in, out uint64
	at      time.Time
}

// NewPoller creates the Proxmox poller.
func NewPoller(deps PollerDeps) *Poller {
	return &Poller{
		deps:      deps,
		syncCh:    make(chan struct{}, 1),
		nodeIdent: map[string]cachedNodeIdentity{},
		guestCfg:  map[string]cachedGuestConfig{},
		netPrev:   map[string]netSample{},
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
	}
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
		connCtx, connCancel := context.WithTimeout(ctx, 45*time.Second)
		p.syncConnector(connCtx, conn)
		connCancel()
	}
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
	resources, err := client.ClusterResources(ctx)
	if err != nil {
		if _, ok := err.(*AuthError); ok {
			p.setStatus(ctx, conn.ID, connectors.StatusAuthFailed, err.Error())
		} else {
			p.setStatus(ctx, conn.ID, connectors.StatusUnreachable, err.Error())
		}
		return
	}

	prefix := connectors.ExternalIDPrefix(conn.Type, conn.Fingerprint)
	nodeMACs := map[string]string{}

	// Hypervisor nodes first so guests can reference their parent MAC.
	for _, r := range resources {
		if r.Type != "node" {
			continue
		}
		mac := p.syncNode(ctx, client, conn, prefix, r)
		if mac != "" {
			nodeMACs[r.Node] = mac
		}
	}
	for _, r := range resources {
		if (r.Type != "qemu" && r.Type != "lxc") || r.Template == 1 {
			continue
		}
		p.syncGuest(ctx, client, conn, prefix, r, nodeMACs[r.Node])
	}

	_ = p.deps.Connectors.UpdateLocalStatus(ctx, conn.ID, connectors.StatusOK, "", time.Now())
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
	p.submitMetrics(ctx, host, cpuM, memM, diskM, nil)
	return mac
}

func (p *Poller) syncGuest(ctx context.Context, client *Client, conn connectors.Connector, prefix string, r Resource, parentMAC string) {
	externalID := fmt.Sprintf("%s%s/%s/%d", prefix, r.Node, r.Type, r.VMID)
	cfg := p.guestIdentity(ctx, client, conn.ID, r)

	mac := externalID // synthetic fallback for NIC-less guests
	if len(cfg.macs) > 0 {
		mac = cfg.macs[0]
	}
	// Linking: if ANY of the guest's NIC MACs matches a registered host (the
	// agent inside the guest), that row becomes the guest — no duplicate.
	existing, stale := p.findExisting(ctx, externalID, cfg.macs)
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

// findExisting resolves the guest to an already-known host row by any of its
// NIC MACs (or the synthetic external-id MAC). When both an agent row and a
// connector-only row match (the agent registered through a different NIC than
// the one we keyed on), the agent row wins and the connector row is returned
// as stale for cleanup.
func (p *Poller) findExisting(ctx context.Context, externalID string, macs []string) (best, stale *hosts.Host) {
	var matches []*hosts.Host
	for _, key := range append([]string{externalID}, macs...) {
		if h, err := p.deps.HostRepo.GetHostByMacAddress(ctx, key); err == nil {
			dup := false
			for _, m := range matches {
				if m.ID == h.ID {
					dup = true
					break
				}
			}
			if !dup {
				matches = append(matches, h)
			}
		}
	}
	for _, h := range matches {
		if hosts.MergeSource(h.Source, hosts.SourceConnector) != hosts.SourceConnector {
			best = h // agent-maintained row wins
			break
		}
	}
	if best == nil {
		if len(matches) > 0 {
			return matches[0], nil
		}
		return nil, nil
	}
	for _, h := range matches {
		if h.ID != best.ID && h.Source == hosts.SourceConnector {
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
		best := -1
		for i, nf := range ifaces {
			if normalizeMAC(nf.HWAddr) == "" {
				continue
			}
			if best == -1 || ifaceScore(nf) > ifaceScore(ifaces[best]) {
				best = i
			}
		}
		if best >= 0 {
			mac = normalizeMAC(ifaces[best].HWAddr)
			ip = strings.SplitN(ifaces[best].Address, "/", 2)[0]
		}
	} else {
		p.deps.Logger.Debug("proxmox: node network unavailable", "node", node, "error", err)
	}

	p.mu.Lock()
	p.nodeIdent[key] = cachedNodeIdentity{mac: mac, ip: ip, fetchedAt: time.Now()}
	p.mu.Unlock()
	return mac, ip
}

func ifaceScore(nf NodeNetIface) int {
	switch {
	case nf.Gateway != "":
		return 3
	case nf.Address != "":
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
		if v, ok := cfg["ostype"].(string); ok {
			out.ostype = v
		}
	} else {
		p.deps.Logger.Debug("proxmox: guest config unavailable", "node", r.Node, "vmid", r.VMID, "error", err)
		out.fetchedAt = time.Now().Add(identityTTL - 30*time.Second) // retry soon
	}

	p.mu.Lock()
	p.guestCfg[key] = out
	p.mu.Unlock()
	return out
}

// upsertHost routes the host write through Raft (replicated) or directly.
// Returns the local row (resolved post-apply) or nil on failure.
func (p *Poller) upsertHost(ctx context.Context, info hosts.ConnectorHostInfo) *hosts.Host {
	if p.deps.Raft != nil && p.deps.Raft.Enabled() {
		if err := p.deps.Raft.SubmitConnectorHostUpsert(ctx, info); err != nil {
			p.deps.Logger.Warn("proxmox: replicate host upsert", "host", info.Name, "error", err)
			return nil
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

	if p.deps.Raft != nil && p.deps.Raft.Enabled() {
		batch := raftcluster.MetricBatchPayload{
			HostMAC:   host.MacAddress,
			HostName:  host.Name,
			Timestamp: ts,
		}
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
		if err := p.deps.Raft.SubmitMetricBatch(ctx, batch); err != nil {
			p.deps.Logger.Debug("proxmox: replicate metrics", "host", host.Name, "error", err)
		}
		return
	}

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
}

func pct(used, total uint64) float64 {
	if total == 0 {
		return 0
	}
	return float64(used) / float64(total) * 100
}
