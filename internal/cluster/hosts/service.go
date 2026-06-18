package hosts

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/log"
)

// hostUpsertHeartbeat is how often a UNCHANGED local host record is still
// pushed through Raft just to refresh last_seen, so peers keep us online with
// a comfortable margin below HostOfflineThreshold. Computed as
// max(15s, HostOfflineThreshold/3): with a 45s threshold that's 15s, leaving
// ~3 heartbeats of slack before a peer would flip us offline. The collector
// ticks every ~5s, so most ticks now skip the consensus round entirely.
var hostUpsertHeartbeat = func() time.Duration {
	if d := HostOfflineThreshold / 3; d > 15*time.Second {
		return d
	}
	return 15 * time.Second
}()

// ErrCannotRemoveLocalHost is returned when a caller tries to remove this
// node's own collector row (id=1). Use the Raft "leave cluster" flow instead.
var ErrCannotRemoveLocalHost = errors.New("cannot remove this node's own host; use leave cluster")

// RaftReplicator is the subset of internal/cluster/raft.Service the hosts
// service needs. Defined locally to avoid an import cycle and to keep
// service tests easy to mock.
type RaftReplicator interface {
	Enabled() bool
	SubmitHostUpsert(ctx context.Context, info HostInfo) error
	// SubmitHostDelete cascades a host removal (row + metrics) cluster-wide,
	// keyed by MAC.
	SubmitHostDelete(ctx context.Context, mac string) error
}

// HostStatics is a host's static hardware identity, read at response time from
// its latest metric rows.
type HostStatics struct {
	CPUModel    string
	CPUCores    int
	MemoryTotal uint64
	DiskTotal   uint64
}

// StaticHardwareSource yields a host's static hardware facts so the /hosts
// response can carry them (so the node card doesn't wait on the per-metric
// queries / SSE for its static scaffold). Defined locally to avoid importing
// the metric packages; the DI layer adapts the cpu/memory/disk repositories to
// it. Nil-safe: when unset, /hosts simply omits these fields.
type StaticHardwareSource interface {
	HostStatics(ctx context.Context, hostID uint) HostStatics
}

// Service defines the hosts service interface.
type Service interface {
	RegisterOrUpdateCurrentHost(ctx context.Context) (*Host, error)
	GetHostByMacAddress(ctx context.Context, macAddress string) (*Host, error)
	GetHostByID(ctx context.Context, id uint) (*Host, error)
	GetAllHosts(ctx context.Context) ([]Host, error)
	GetCurrentHost(ctx context.Context) (*Host, error)
	GetCurrentHostInfo(ctx context.Context) (HostInfo, error)
	// RemoveHost deletes a host row and all its metrics. When Raft is enabled
	// the removal is replicated cluster-wide (by MAC); otherwise it is local.
	// Removing the local collector row (id=1) is rejected.
	RemoveHost(ctx context.Context, id uint) error
}

type service struct {
	logger         *log.Logger
	collector      *HostCollector
	hostRepository Repository
	raft           RaftReplicator
	statics        StaticHardwareSource

	// upsertMu guards the throttling state for the periodic Raft host upsert.
	upsertMu sync.Mutex
	// lastUpsertHash is the fingerprint of the last HostInfo submitted through
	// Raft, EXCLUDING last_seen — so an unchanged record doesn't trigger a
	// consensus round every 5s metrics tick.
	lastUpsertHash [sha256.Size]byte
	// lastUpsertSubmit is when we last submitted (changed record or heartbeat).
	lastUpsertSubmit time.Time
	// haveUpserted records whether any submission has happened yet.
	haveUpserted bool
}

// NewService creates a new hosts service.
func NewService(logger *log.Logger, repo Repository) Service {
	return &service{
		logger:         logger,
		collector:      newHostCollector(logger),
		hostRepository: repo,
	}
}

// WithRaftReplicator attaches a Raft replicator so that registering the
// current host also publishes a CmdHostUpsert. Call after construction;
// nil disables replication and falls back to the legacy direct-write path.
func (s *service) WithRaftReplicator(r RaftReplicator) Service {
	s.raft = r
	return s
}

// AttachRaftReplicator is the package-level helper used by DI when the
// service has already been wired into other components.
func AttachRaftReplicator(svc Service, r RaftReplicator) {
	if impl, ok := svc.(*service); ok {
		impl.raft = r
	}
}

// AttachStaticHardwareSource wires the latest-metrics adapter used to enrich
// the /hosts response with each host's static hardware identity.
func AttachStaticHardwareSource(svc Service, src StaticHardwareSource) {
	if impl, ok := svc.(*service); ok {
		impl.statics = src
	}
}

// enrichStatics fills a host's static hardware fields from its latest metrics.
func (s *service) enrichStatics(ctx context.Context, h *Host) {
	if s.statics == nil || h == nil {
		return
	}
	st := s.statics.HostStatics(ctx, h.ID)
	h.CPUModel = st.CPUModel
	h.CPUCores = st.CPUCores
	h.MemoryTotal = st.MemoryTotal
	h.DiskTotal = st.DiskTotal
}

func (s *service) RegisterOrUpdateCurrentHost(ctx context.Context) (*Host, error) {
	s.logger.Debug("Registering or updating local collector host", "host_id", LocalCollectorHostID)

	// CollectHostInfo scans OS and network interfaces; give it its own deadline
	// so a slow IO-counter syscall (common on macOS with many Docker networks)
	// does not consume the caller's DB-operation budget.
	collectCtx, collectCancel := context.WithTimeout(context.Background(), 15*time.Second)
	hostInfo, err := s.collector.CollectHostInfo(collectCtx)
	collectCancel()
	if err != nil {
		s.logger.Error("Failed to collect host information", "error", err)
		return nil, err
	}

	host, err := s.hostRepository.UpsertLocalHost(ctx, hostInfo)
	if err != nil {
		s.logger.Error("Failed to upsert local host record", "error", err)
		return nil, err
	}

	// When the Raft layer is enabled also publish this node into the
	// replicated host registry so every other node in this cluster (and,
	// after the bridge ships entries, the peer cluster) sees us in
	// /api/v1/hosts. Best-effort: a failure here does not block the
	// local id=1 collector row from being written.
	//
	// Throttled: this runs on every ~5s metrics tick, but a CmdHostUpsert is a
	// full consensus round. We only submit when the host record actually
	// changed (hostname/IP/topology/etc.) OR enough time has elapsed that peers
	// would soon flip us offline — see shouldSubmitUpsert. This keeps Raft log
	// growth proportional to real change, not wall-clock, while still keeping
	// every node green with a comfortable margin.
	if s.raft != nil && s.raft.Enabled() && s.shouldSubmitUpsert(hostInfo, time.Now()) {
		if rerr := s.raft.SubmitHostUpsert(ctx, hostInfo); rerr != nil {
			s.logger.Warn("Raft host upsert failed", "error", rerr)
		}
	}

	s.logger.Debug("Local host registered/updated", "host_id", host.ID, "name", host.Name, "mac", host.MacAddress)
	return host, nil
}

// hostInfoFingerprint hashes every identity/topology field of a HostInfo.
// HostInfo carries no last_seen (the repository stamps that), so the whole
// struct IS "the record excluding last_seen": a stable fingerprint changes
// only when something a peer cares about (hostname, IP, kernel, boot time, …)
// actually changes.
func hostInfoFingerprint(info HostInfo) [sha256.Size]byte {
	// A field separator that can't appear in the values keeps distinct field
	// boundaries unambiguous (so "ab"+"c" != "a"+"bc").
	s := strings.Join([]string{
		info.OriginCluster,
		info.Name,
		info.MacAddress,
		info.IPv4,
		info.OS,
		info.Platform,
		info.PlatformFamily,
		info.PlatformVersion,
		info.KernelVersion,
		info.VirtualizationSystem,
		info.VirtualizationRole,
		info.HostID,
		info.HardwareUUID,
		fmt.Sprintf("%d", info.BootTime),
	}, "\x00")
	return sha256.Sum256([]byte(s))
}

// shouldSubmitUpsert reports whether the periodic local-host registration
// should issue a Raft CmdHostUpsert this tick, and records the decision. It
// submits when the record changed since the last submission, or when at least
// hostUpsertHeartbeat has elapsed (a liveness refresh so peers keep us online).
// Mutex-protected because the collection loop and any concurrent register call
// can both reach here.
func (s *service) shouldSubmitUpsert(info HostInfo, now time.Time) bool {
	fp := hostInfoFingerprint(info)

	s.upsertMu.Lock()
	defer s.upsertMu.Unlock()

	changed := !s.haveUpserted || fp != s.lastUpsertHash
	elapsed := now.Sub(s.lastUpsertSubmit) >= hostUpsertHeartbeat
	if !changed && !elapsed {
		return false
	}
	s.lastUpsertHash = fp
	s.lastUpsertSubmit = now
	s.haveUpserted = true
	return true
}

func (s *service) GetHostByID(ctx context.Context, id uint) (*Host, error) {
	s.logger.Debug("Getting host by ID", "host_id", id)
	host, err := s.hostRepository.GetHostByID(ctx, id)
	if err != nil {
		s.logger.Error("Failed to get host by ID", "error", err, "host_id", id)
		return nil, err
	}
	return host, nil
}

func (s *service) GetHostByMacAddress(ctx context.Context, macAddress string) (*Host, error) {
	s.logger.Debug("Getting host by MAC address", "mac_address", macAddress)
	host, err := s.hostRepository.GetHostByMacAddress(ctx, macAddress)
	if err != nil {
		s.logger.Error("Failed to get host by MAC address", "error", err, "mac_address", macAddress)
		return nil, err
	}
	s.logger.Debug("Host retrieved successfully", "host_id", host.ID, "name", host.Name)
	return host, nil
}

func (s *service) GetAllHosts(ctx context.Context) ([]Host, error) {
	s.logger.Debug("Getting all hosts")
	hosts, err := s.hostRepository.GetAllHosts(ctx)
	if err != nil {
		s.logger.Error("Failed to get all hosts", "error", err)
		return nil, err
	}
	if dn := strings.TrimSpace(os.Getenv("NODE_STATS_HOSTNAME")); dn != "" {
		for i := range hosts {
			if hosts[i].ID == LocalCollectorHostID {
				hosts[i].DisplayName = dn
				break
			}
		}
	}
	// Resolve parent_mac → local parent_id for the UI (row ids differ per node;
	// only the MAC is cluster-stable).
	macToID := make(map[string]uint, len(hosts))
	for _, h := range hosts {
		if h.MacAddress != "" {
			macToID[strings.ToLower(h.MacAddress)] = h.ID
		}
	}
	for i := range hosts {
		if pm := strings.ToLower(hosts[i].ParentMAC); pm != "" {
			hosts[i].ParentID = macToID[pm]
		}
	}
	// Static hardware identity from each host's latest metrics, so the node
	// card gets cpu model/cores + ram/disk totals in this single request.
	for i := range hosts {
		s.enrichStatics(ctx, &hosts[i])
	}
	s.logger.Debug("All hosts retrieved successfully", "count", len(hosts))
	return hosts, nil
}

func (s *service) RemoveHost(ctx context.Context, id uint) error {
	if id == LocalCollectorHostID {
		return ErrCannotRemoveLocalHost
	}
	host, err := s.hostRepository.GetHostByID(ctx, id)
	if err != nil {
		s.logger.Error("Failed to load host for removal", "error", err, "host_id", id)
		return err
	}
	// Cluster-wide removal keyed by MAC when Raft is enabled — the FSM applier
	// on every node (including this one) resolves the MAC to its local row and
	// cascades. Standalone falls back to a direct local cascade delete.
	if s.raft != nil && s.raft.Enabled() {
		if host.MacAddress == "" {
			// A replicated delete is keyed by MAC; without one the applier would
			// silently no-op on every node. Refuse rather than delete only locally.
			return fmt.Errorf("host %d (%q) has no MAC address; cannot remove cluster-wide", id, host.Name)
		}
		s.logger.Info("Removing host cluster-wide", "host_id", id, "name", host.Name, "mac", host.MacAddress)
		return s.raft.SubmitHostDelete(ctx, host.MacAddress)
	}
	s.logger.Info("Removing host locally", "host_id", id, "name", host.Name)
	return s.hostRepository.DeleteHostCascade(ctx, id)
}

func (s *service) GetCurrentHost(ctx context.Context) (*Host, error) {
	host, err := s.hostRepository.GetHostByID(ctx, LocalCollectorHostID)
	if err != nil {
		return nil, err
	}
	s.enrichStatics(ctx, host)
	return host, nil
}

func (s *service) GetCurrentHostInfo(ctx context.Context) (HostInfo, error) {
	return s.collector.CollectHostInfo(ctx)
}
