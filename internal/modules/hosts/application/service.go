package hosts

import (
	"context"
	"errors"

	"github.com/charmbracelet/log"
	"gorm.io/gorm"

	"system-stats/internal/modules/hosts/infrastructure/collectors"
	"system-stats/internal/modules/hosts/infrastructure/entities"
	hostrepos "system-stats/internal/modules/hosts/infrastructure/repositories"
)

type Service interface {
	RegisterOrUpdateCurrentHost(ctx context.Context) (*entities.Host, error)
	GetHostByMacAddress(ctx context.Context, macAddress string) (*entities.Host, error)
	GetAllHosts(ctx context.Context) ([]entities.Host, error)
	GetCurrentHost(ctx context.Context) (*entities.Host, error)
}

type service struct {
	logger         *log.Logger
	collector      *collectors.HostCollector
	hostRepository hostrepos.HostRepository
}

func NewService(logger *log.Logger, hostRepository hostrepos.HostRepository) Service {
	return &service{
		logger:         logger,
		collector:      collectors.NewHostCollector(logger),
		hostRepository: hostRepository,
	}
}

func (s *service) RegisterOrUpdateCurrentHost(ctx context.Context) (*entities.Host, error) {
	s.logger.Info("Registering or updating current host")

	// Collect current host information
	hostInfo, err := s.collector.CollectHostInfo(ctx)
	if err != nil {
		s.logger.Error("Failed to collect host information", "error", err)
		return nil, err
	}

	// Upsert host record
	host, err := s.hostRepository.UpsertHost(ctx, hostInfo)
	if err != nil {
		s.logger.Error("Failed to upsert host record", "error", err)
		return nil, err
	}

	s.logger.Info("Host registered/updated successfully", "host_id", host.ID, "name", host.Name, "mac", host.MacAddress)
	return host, nil
}

func (s *service) GetHostByMacAddress(ctx context.Context, macAddress string) (*entities.Host, error) {
	s.logger.Info("Getting host by MAC address", "mac_address", macAddress)
	host, err := s.hostRepository.GetHostByMacAddress(ctx, macAddress)
	if err != nil {
		s.logger.Error("Failed to get host by MAC address", "error", err, "mac_address", macAddress)
		return nil, err
	}
	s.logger.Info("Host retrieved successfully", "host_id", host.ID, "name", host.Name)
	return host, nil
}

func (s *service) GetAllHosts(ctx context.Context) ([]entities.Host, error) {
	s.logger.Info("Getting all hosts")
	hosts, err := s.hostRepository.GetAllHosts(ctx)
	if err != nil {
		s.logger.Error("Failed to get all hosts", "error", err)
		return nil, err
	}
	s.logger.Info("All hosts retrieved successfully", "count", len(hosts))
	return hosts, nil
}

func (s *service) GetCurrentHost(ctx context.Context) (*entities.Host, error) {
	s.logger.Info("Getting current host information")

	// Collect current host information to get MAC address
	hostInfo, err := s.collector.CollectHostInfo(ctx)
	if err != nil {
		s.logger.Error("Failed to collect current host information", "error", err)
		return nil, err
	}

	// Try to get host by MAC; if not found, upsert it
	host, err := s.hostRepository.GetHostByMacAddress(ctx, hostInfo.MacAddress)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			s.logger.Info("Host not found by MAC, performing upsert", "mac_address", hostInfo.MacAddress)
			return s.hostRepository.UpsertHost(ctx, hostInfo)
		}
		s.logger.Error("Failed to get host by MAC address", "error", err, "mac_address", hostInfo.MacAddress)
		return nil, err
	}

	return host, nil
}
