package hosts

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/log"
)

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
	if s.raft != nil && s.raft.Enabled() {
		if rerr := s.raft.SubmitHostUpsert(ctx, hostInfo); rerr != nil {
			s.logger.Warn("Raft host upsert failed", "error", rerr)
		}
	}

	s.logger.Debug("Local host registered/updated", "host_id", host.ID, "name", host.Name, "mac", host.MacAddress)
	return host, nil
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
	return s.hostRepository.GetHostByID(ctx, LocalCollectorHostID)
}

func (s *service) GetCurrentHostInfo(ctx context.Context) (HostInfo, error) {
	return s.collector.CollectHostInfo(ctx)
}
