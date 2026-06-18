package pbs

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/log"

	hosts "system-stats/internal/cluster/hosts"
	raftcluster "system-stats/internal/cluster/raft"
	cpu "system-stats/internal/metrics/cpu"
	disk "system-stats/internal/metrics/disk"
	memory "system-stats/internal/metrics/memory"
	connectors "system-stats/internal/platform/connectors"
)

const (
	pollInterval        = 10 * time.Second
	hostUpsertHeartbeat = 60 * time.Second
	// recentTaskCap bounds the backup-health task summary kept per PBS host.
	recentTaskCap = 8
)

// PollerDeps bundles everything the PBS poller needs. Mirrors the Proxmox
// poller: when Raft is active host upserts go through the replicated log; when
// standalone they fall back to direct repository writes + a local SSE publish.
type PollerDeps struct {
	Logger     *log.Logger
	Connectors connectors.Repository
	Cipher     *connectors.Cipher
	HostRepo   hosts.Repository

	Raft    *raftcluster.Replicator
	RaftSvc raftcluster.Service
	CPURepo cpu.Repository
	MemRepo memory.Repository
	DiskRepo disk.Repository
	Publish func([]byte)
	BroadcastMetrics func(context.Context, raftcluster.MetricBatchPayload)
}

// Datastore is one PBS datastore's capacity.
type Datastore struct {
	Store        string  `json:"store"`
	Total        uint64  `json:"total"`
	Used         uint64  `json:"used"`
	Avail        uint64  `json:"avail"`
	UsagePercent float64 `json:"usage_percent"`
	Error        string  `json:"error,omitempty"`
}

// TaskSummary is one recent PBS task for the backup-health view.
type TaskSummary struct {
	Type      string `json:"type"`
	ID        string `json:"id,omitempty"`
	StartTime int64  `json:"start_time"`
	Status    string `json:"status"` // "OK" | "running" | error message
}

// BackupHealth summarizes recent backup/verify/GC activity on a PBS host.
type BackupHealth struct {
	LastBackupAt int64         `json:"last_backup_at,omitempty"`
	LastErrorAt  int64         `json:"last_error_at,omitempty"`
	LastError    string        `json:"last_error,omitempty"`
	RunningTasks int           `json:"running_tasks"`
	RecentTasks  []TaskSummary `json:"recent_tasks,omitempty"`
}

// BackupGroup is one backed-up machine on a datastore (a vm/ct/host group) with
// its most-recent backup time, that backup's size, snapshot count, and the
// machine name read from the newest snapshot's notes (PVE writes the guest name
// there by default, so backups can show names instead of bare VMIDs).
type BackupGroup struct {
	Datastore  string `json:"datastore"`
	BackupType string `json:"backup_type"` // vm | ct | host
	BackupID   string `json:"backup_id"`
	Name       string `json:"name,omitempty"` // guest name from the newest snapshot's notes
	LastBackup int64  `json:"last_backup,omitempty"`
	LastSize   uint64 `json:"last_size,omitempty"` // bytes — size of the newest snapshot
	Count      int    `json:"count"`
}

// Status is the PBS-specific detail surfaced by GET /pbs?host_id= for the
// backup-server widget (datastore capacity, backed-up machines, backup health).
// It is a live snapshot kept by the polling node (leader); node CPU/mem/disk
// vitals flow through the normal replicated metric pipeline instead.
type Status struct {
	HostID     uint          `json:"host_id"`
	Datastores []Datastore   `json:"datastores"`
	Groups     []BackupGroup `json:"groups"` // backed-up machines + last-backup time
	Backups    BackupHealth  `json:"backups"`
	UpdatedAt  time.Time     `json:"updated_at"`
}

// Poller drives every enabled PBS connector. Exactly one node polls in a
// cluster — the Raft leader (a standalone node always polls).
type Poller struct {
	deps   PollerDeps
	syncCh chan struct{}

	mu          sync.Mutex
	status      map[uint]Status            // host id → latest detail snapshot
	upsertState map[string]hostUpsertState // external_id → throttle state
}

type hostUpsertState struct {
	hash       [sha256.Size]byte
	lastSubmit time.Time
}

// NewPoller creates the PBS poller.
func NewPoller(deps PollerDeps) *Poller {
	return &Poller{
		deps:        deps,
		syncCh:      make(chan struct{}, 1),
		status:      map[uint]Status{},
		upsertState: map[string]hostUpsertState{},
	}
}

// TriggerSync requests an immediate cycle (admin "sync now" / right after
// connecting). Non-blocking.
func (p *Poller) TriggerSync() {
	select {
	case p.syncCh <- struct{}{}:
	default:
	}
}

// Snapshot returns the latest datastore/backup detail for a PBS host (the
// polling node only). ok is false on a node that isn't polling this host.
func (p *Poller) Snapshot(hostID uint) (Status, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	s, ok := p.status[hostID]
	return s, ok
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
// cluster sees exactly one writer per connector. Mirrors the Proxmox poller.
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
		p.deps.Logger.Warn("pbs: list connectors", "error", err)
		return
	}
	live := map[uint]struct{}{}
	for _, conn := range conns {
		if conn.Type != connectors.TypePBS {
			continue
		}
		if !conn.Enabled {
			if conn.Status != connectors.StatusDisabled {
				_ = p.deps.Connectors.UpdateLocalStatus(ctx, conn.ID, connectors.StatusDisabled, "", time.Time{})
			}
			continue
		}
		connCtx, connCancel := context.WithTimeout(ctx, 30*time.Second)
		if id := p.syncConnector(connCtx, conn); id != 0 {
			live[id] = struct{}{}
		}
		connCancel()
	}
	// Drop snapshots for connectors no longer enabled/present.
	p.mu.Lock()
	for id := range p.status {
		if _, ok := live[id]; !ok {
			delete(p.status, id)
		}
	}
	p.mu.Unlock()
}

// syncConnector polls one PBS connector: upserts its host row, feeds node
// metrics, and refreshes the datastore/backup snapshot. Returns the host id.
func (p *Poller) syncConnector(ctx context.Context, conn connectors.Connector) uint {
	secret, err := p.deps.Cipher.Decrypt(conn.SecretEnc)
	if err != nil {
		p.setStatus(ctx, conn.ID, connectors.StatusAuthFailed, "cannot decrypt secret (JWT secret changed?): "+err.Error())
		return 0
	}
	client, err := NewClient(conn.Endpoint, conn.TokenID, secret, conn.SkipTLSVerify)
	if err != nil {
		p.setStatus(ctx, conn.ID, connectors.StatusUnreachable, err.Error())
		return 0
	}
	defer client.Close()

	node := client.NodeName(ctx)
	status, err := client.NodeStatus(ctx, node)
	if err != nil {
		if _, ok := err.(*AuthError); ok {
			p.setStatus(ctx, conn.ID, connectors.StatusAuthFailed, err.Error())
		} else {
			p.setStatus(ctx, conn.ID, connectors.StatusUnreachable, err.Error())
		}
		return 0
	}

	prefix := connectors.ExternalIDPrefix(conn.Type, conn.Fingerprint)
	externalID := prefix + node
	version, _ := client.Version(ctx)

	info := hosts.ConnectorHostInfo{
		HostInfo: hosts.HostInfo{
			Name:            pbsHostName(node, client.Host()),
			MacAddress:      externalID, // synthetic — PBS exposes no NIC MAC
			IPv4:            ipFromHost(client.Host()),
			OS:              "linux",
			Platform:        "proxmox-backup-server",
			PlatformVersion: version,
			KernelVersion:   strings.TrimSpace(status.KVersion),
		},
		ExternalID:  externalID,
		GuestStatus: "online",
	}
	if status.Uptime > 0 {
		info.BootTime = time.Now().Unix() - status.Uptime
	}

	host := p.upsertHost(ctx, info)
	if host == nil {
		return 0
	}

	// Node vitals → normal replicated metric pipeline (the machine card).
	cpuM := &cpu.CPUMetric{
		UsagePercent: status.CPU * 100,
		Cores:        status.CPUInfo.CPUs,
		ModelName:    status.CPUInfo.Model,
	}
	load := status.LoadAvgFloats()
	cpuM.LoadAvg1, cpuM.LoadAvg5, cpuM.LoadAvg15 = load[0], load[1], load[2]
	memM := &memory.MemoryMetric{
		Total:        status.Memory.Total,
		Used:         status.Memory.Used,
		Free:         status.Memory.Free,
		Available:    status.Memory.Free,
		UsagePercent: pct(status.Memory.Used, status.Memory.Total),
	}
	diskM := &disk.DiskMetric{
		Total:        status.Root.Total,
		Used:         status.Root.Used,
		Free:         status.Root.Avail,
		UsagePercent: pct(status.Root.Used, status.Root.Total),
	}
	p.submitMetrics(ctx, host, cpuM, memM, diskM)

	// PBS-specific detail (datastores + backup health) → in-memory snapshot.
	p.refreshDetail(ctx, client, node, host.ID)

	_ = p.deps.Connectors.UpdateLocalStatus(ctx, conn.ID, connectors.StatusOK, "", time.Now())
	return host.ID
}

// refreshDetail fetches datastore usage + recent tasks and stores the snapshot.
func (p *Poller) refreshDetail(ctx context.Context, client *Client, node string, hostID uint) {
	var datastores []Datastore
	if usage, err := client.DatastoreUsage(ctx); err == nil {
		for _, u := range usage {
			datastores = append(datastores, Datastore{
				Store: u.Store, Total: u.Total, Used: u.Used, Avail: u.Avail,
				UsagePercent: pct(u.Used, u.Total), Error: u.Error,
			})
		}
	} else {
		p.deps.Logger.Debug("pbs: datastore usage unavailable", "error", err)
	}

	// Backed-up machines: backup groups per datastore (newest backup first). The
	// /groups endpoint omits sizes, so we size each group by its newest snapshot.
	var groups []BackupGroup
	for _, ds := range datastores {
		gs, err := client.DatastoreGroups(ctx, ds.Store)
		if err != nil {
			p.deps.Logger.Debug("pbs: datastore groups unavailable", "store", ds.Store, "error", err)
			continue
		}
		var latest map[string]snapshotInfo
		if snaps, err := client.DatastoreSnapshots(ctx, ds.Store); err == nil {
			latest = latestSnapshotInfo(snaps)
		} else {
			p.deps.Logger.Debug("pbs: datastore snapshots unavailable", "store", ds.Store, "error", err)
		}
		for _, g := range gs {
			info := latest[g.BackupType+"/"+g.BackupID]
			groups = append(groups, BackupGroup{
				Datastore: ds.Store, BackupType: g.BackupType, BackupID: g.BackupID,
				Name: info.name, LastBackup: g.LastBackup, LastSize: info.size, Count: g.BackupCount,
			})
		}
	}
	sort.SliceStable(groups, func(i, j int) bool { return groups[i].LastBackup > groups[j].LastBackup })

	var backups BackupHealth
	if tasks, err := client.Tasks(ctx, node, 100); err == nil {
		backups = buildBackupHealth(tasks)
	} else {
		p.deps.Logger.Debug("pbs: tasks unavailable", "error", err)
	}

	p.mu.Lock()
	p.status[hostID] = Status{HostID: hostID, Datastores: datastores, Groups: groups, Backups: backups, UpdatedAt: time.Now()}
	p.mu.Unlock()
}

// snapshotInfo carries the per-group detail taken from a group's newest snapshot.
type snapshotInfo struct {
	size uint64
	name string // guest name parsed from the snapshot's notes/comment
}

// latestSnapshotInfo maps "type/id" → the size and name of that group's newest
// snapshot, so each backed-up machine can show its last backup's size and a
// human name (PVE stores the guest name in the snapshot notes by default).
func latestSnapshotInfo(snaps []Snapshot) map[string]snapshotInfo {
	latestTime := map[string]int64{}
	out := map[string]snapshotInfo{}
	for _, s := range snaps {
		key := s.BackupType + "/" + s.BackupID
		if t, seen := latestTime[key]; !seen || s.BackupTime >= t {
			latestTime[key] = s.BackupTime
			out[key] = snapshotInfo{size: s.Size, name: snapshotName(s.Comment)}
		}
	}
	return out
}

// snapshotName extracts a display name from a snapshot's notes: the first line,
// trimmed. PVE's default notes-template "{{guestname}}" makes this the guest
// name; empty when no usable note is set (UI then falls back to the VMID).
func snapshotName(comment string) string {
	line := comment
	if i := strings.IndexAny(line, "\r\n"); i >= 0 {
		line = line[:i]
	}
	return strings.TrimSpace(line)
}

// buildBackupHealth classifies recent tasks into last-backup time, last error,
// running count, and a capped recent list. PBS returns tasks newest-first.
func buildBackupHealth(tasks []Task) BackupHealth {
	var h BackupHealth
	relevant := map[string]bool{"backup": true, "verify": true, "garbage_collection": true, "prune": true, "sync": true}
	// Newest-first; sort defensively by start time descending.
	sort.SliceStable(tasks, func(i, j int) bool { return tasks[i].StartTime > tasks[j].StartTime })
	for _, t := range tasks {
		if !relevant[t.Type] {
			continue
		}
		running := t.EndTime == 0 && t.Status == ""
		state := t.Status
		switch {
		case running:
			h.RunningTasks++
			state = "running"
		case t.Status == "OK":
			if t.Type == "backup" && h.LastBackupAt == 0 {
				h.LastBackupAt = t.StartTime
			}
		default: // finished with an error
			if h.LastErrorAt == 0 {
				h.LastErrorAt = t.StartTime
				h.LastError = t.Status
			}
		}
		if len(h.RecentTasks) < recentTaskCap {
			h.RecentTasks = append(h.RecentTasks, TaskSummary{Type: t.Type, ID: t.ID, StartTime: t.StartTime, Status: state})
		}
	}
	return h
}

func (p *Poller) setStatus(ctx context.Context, id uint, status, msg string) {
	p.deps.Logger.Warn("pbs: connector sync failed", "connector_id", id, "status", status, "error", msg)
	_ = p.deps.Connectors.UpdateLocalStatus(ctx, id, status, msg, time.Time{})
}

// upsertHost routes the host write through Raft (replicated) or directly,
// throttled per host (unchanged identity within the heartbeat skips a round).
func (p *Poller) upsertHost(ctx context.Context, info hosts.ConnectorHostInfo) *hosts.Host {
	if p.deps.Raft != nil && p.deps.Raft.Enabled() {
		if p.shouldSubmitHostUpsert(info, time.Now()) {
			if err := p.deps.Raft.SubmitConnectorHostUpsert(ctx, info); err != nil {
				p.deps.Logger.Warn("pbs: replicate host upsert", "host", info.Name, "error", err)
				return nil
			}
		}
	} else {
		if _, err := p.deps.HostRepo.UpsertConnectorHost(ctx, info); err != nil {
			p.deps.Logger.Warn("pbs: upsert host", "host", info.Name, "error", err)
			return nil
		}
	}
	host, err := p.deps.HostRepo.GetHostByMacAddress(ctx, info.MacAddress)
	if err != nil {
		return nil
	}
	return host
}

func (p *Poller) shouldSubmitHostUpsert(info hosts.ConnectorHostInfo, now time.Time) bool {
	key := info.ExternalID
	if key == "" {
		key = info.MacAddress
	}
	fp := hostFingerprint(info)
	p.mu.Lock()
	defer p.mu.Unlock()
	prev, seen := p.upsertState[key]
	changed := !seen || fp != prev.hash
	elapsed := now.Sub(prev.lastSubmit) >= hostUpsertHeartbeat
	if seen && !changed && !elapsed {
		return false
	}
	p.upsertState[key] = hostUpsertState{hash: fp, lastSubmit: now}
	return true
}

func hostFingerprint(info hosts.ConnectorHostInfo) [sha256.Size]byte {
	s := strings.Join([]string{
		info.Name, info.MacAddress, info.IPv4, info.Platform,
		info.PlatformVersion, info.KernelVersion, fmt.Sprintf("%d", info.BootTime),
		info.ExternalID, info.GuestStatus,
	}, "\x00")
	return sha256.Sum256([]byte(s))
}

// submitMetrics replicates a metric batch (Raft fanout) or saves + publishes
// locally. Mirrors the Proxmox poller's metric path.
func (p *Poller) submitMetrics(ctx context.Context, host *hosts.Host, cpuM *cpu.CPUMetric, memM *memory.MemoryMetric, diskM *disk.DiskMetric) {
	ts := time.Now().UTC()
	marshal := func(v any) json.RawMessage {
		b, err := json.Marshal(v)
		if err != nil {
			return nil
		}
		return b
	}
	env := map[string]any{"collecting_host_id": host.ID, "timestamp": ts}
	if err := p.deps.CPURepo.SaveCurrentMetricAt(ctx, *cpuM, host.ID, ts); err == nil {
		env["cpu"] = cpuM
	}
	if err := p.deps.MemRepo.SaveCurrentMetricAt(ctx, *memM, host.ID, ts); err == nil {
		env["memory"] = memM
	}
	if err := p.deps.DiskRepo.SaveCurrentMetricAt(ctx, *diskM, host.ID, ts); err == nil {
		env["disk"] = diskM
	}
	if p.deps.Publish != nil {
		if b, err := json.Marshal(env); err == nil {
			p.deps.Publish(b)
		}
	}
	if p.deps.BroadcastMetrics != nil {
		p.deps.BroadcastMetrics(ctx, raftcluster.MetricBatchPayload{
			HostMAC: host.MacAddress, HostName: host.Name, Timestamp: ts,
			CPU: marshal(cpuM), Memory: marshal(memM), Disk: marshal(diskM),
		})
	}
}

// pbsHostName picks a friendly display name: the PBS node name, or the endpoint
// host when the node is the default "localhost".
func pbsHostName(node, host string) string {
	if node != "" && node != "localhost" {
		return node
	}
	if h, _, err := net.SplitHostPort(host); err == nil && h != "" {
		return h
	}
	if host != "" {
		return host
	}
	return "pbs"
}

// ipFromHost returns the host portion of "host:port" when it's an IP literal.
func ipFromHost(host string) string {
	h := host
	if x, _, err := net.SplitHostPort(host); err == nil {
		h = x
	}
	if net.ParseIP(h) != nil {
		return h
	}
	return ""
}

func pct(used, total uint64) float64 {
	if total == 0 {
		return 0
	}
	return float64(used) / float64(total) * 100
}
