package hosts

import (
	"context"
	"os"
	"strings"

	"github.com/charmbracelet/log"

	"system-stats/internal/modules/hosts/infrastructure/collectors"
	"system-stats/internal/modules/hosts/infrastructure/entities"
	hostrepos "system-stats/internal/modules/hosts/infrastructure/repositories"
)

// nodePushCredentialSource lists which hosts have a cluster push token on this main.
type nodePushCredentialSource interface {
	HostIDsWithPushCredential(ctx context.Context) (map[uint]struct{}, error)
}

type Service interface {
	RegisterOrUpdateCurrentHost(ctx context.Context) (*entities.Host, error)
	GetHostByMacAddress(ctx context.Context, macAddress string) (*entities.Host, error)
	GetHostByID(ctx context.Context, id uint) (*entities.Host, error)
	GetAllHosts(ctx context.Context) ([]entities.Host, error)
	GetCurrentHost(ctx context.Context) (*entities.Host, error)
	GetCurrentHostInfo(ctx context.Context) (entities.HostInfo, error)
}

type service struct {
	logger         *log.Logger
	collector      *collectors.HostCollector
	hostRepository hostrepos.HostRepository
	nodePushCreds  nodePushCredentialSource
}

func NewService(logger *log.Logger, hostRepository hostrepos.HostRepository, nodePushCreds nodePushCredentialSource) Service {
	return &service{
		logger:         logger,
		collector:      collectors.NewHostCollector(logger),
		hostRepository: hostRepository,
		nodePushCreds:  nodePushCreds,
	}
}

func (s *service) RegisterOrUpdateCurrentHost(ctx context.Context) (*entities.Host, error) {
	s.logger.Debug("Registering or updating local collector host", "host_id", entities.LocalCollectorHostID)

	hostInfo, err := s.collector.CollectHostInfo(ctx)
	if err != nil {
		s.logger.Error("Failed to collect host information", "error", err)
		return nil, err
	}

	host, err := s.hostRepository.UpsertLocalHost(ctx, hostInfo)
	if err != nil {
		s.logger.Error("Failed to upsert local host record", "error", err)
		return nil, err
	}

	s.logger.Debug("Local host registered/updated", "host_id", host.ID, "name", host.Name, "mac", host.MacAddress)
	return host, nil
}

func (s *service) GetHostByID(ctx context.Context, id uint) (*entities.Host, error) {
	s.logger.Debug("Getting host by ID", "host_id", id)
	host, err := s.hostRepository.GetHostByID(ctx, id)
	if err != nil {
		s.logger.Error("Failed to get host by ID", "error", err, "host_id", id)
		return nil, err
	}
	return host, nil
}

func (s *service) GetHostByMacAddress(ctx context.Context, macAddress string) (*entities.Host, error) {
	s.logger.Debug("Getting host by MAC address", "mac_address", macAddress)
	host, err := s.hostRepository.GetHostByMacAddress(ctx, macAddress)
	if err != nil {
		s.logger.Error("Failed to get host by MAC address", "error", err, "mac_address", macAddress)
		return nil, err
	}
	s.logger.Debug("Host retrieved successfully", "host_id", host.ID, "name", host.Name)
	return host, nil
}

func (s *service) GetAllHosts(ctx context.Context) ([]entities.Host, error) {
	s.logger.Debug("Getting all hosts")
	hosts, err := s.hostRepository.GetAllHosts(ctx)
	if err != nil {
		s.logger.Error("Failed to get all hosts", "error", err)
		return nil, err
	}
	if s.nodePushCreds != nil {
		credHosts, err := s.nodePushCreds.HostIDsWithPushCredential(ctx)
		if err != nil {
			s.logger.Error("Failed to list node credential host IDs", "error", err)
			return nil, err
		}
		for i := range hosts {
			if _, ok := credHosts[hosts[i].ID]; ok {
				hosts[i].HasNodeCredential = true
			}
		}
	}
	if dn := strings.TrimSpace(os.Getenv("NODE_STATS_HOSTNAME")); dn != "" {
		for i := range hosts {
			if hosts[i].ID == entities.LocalCollectorHostID {
				hosts[i].DisplayName = dn
				break
			}
		}
	}
	s.logger.Debug("All hosts retrieved successfully", "count", len(hosts))
	return hosts, nil
}

func (s *service) GetCurrentHost(ctx context.Context) (*entities.Host, error) {
	s.logger.Debug("Getting current (local collector) host")
	hostInfo, err := s.collector.CollectHostInfo(ctx)
	if err != nil {
		s.logger.Error("Failed to collect current host information", "error", err)
		return nil, err
	}
	return s.hostRepository.UpsertLocalHost(ctx, hostInfo)
}

func (s *service) GetCurrentHostInfo(ctx context.Context) (entities.HostInfo, error) {
	return s.collector.CollectHostInfo(ctx)
}
