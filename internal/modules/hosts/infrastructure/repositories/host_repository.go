package repositories

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"

	cpuentities "system-stats/internal/modules/cpu/infrastructure/entities"
	diskentities "system-stats/internal/modules/disk/infrastructure/entities"
	dockerdomain "system-stats/internal/modules/docker/domain/repositories"
	dockerentities "system-stats/internal/modules/docker/infrastructure/entities"
	localentities "system-stats/internal/modules/hosts/infrastructure/entities"
	memoryentities "system-stats/internal/modules/memory/infrastructure/entities"
	networkentities "system-stats/internal/modules/network/infrastructure/entities"
	nodeentities "system-stats/internal/modules/nodes/infrastructure/entities"
)

type HostRepository interface {
	// UpsertLocalHost updates the fixed local collector row (id = LocalCollectorHostID).
	UpsertLocalHost(ctx context.Context, hostInfo localentities.HostInfo) (*localentities.Host, error)
	UpsertHost(ctx context.Context, hostInfo localentities.HostInfo) (*localentities.Host, error)
	GetHostByMacAddress(ctx context.Context, macAddress string) (*localentities.Host, error)
	GetHostByID(ctx context.Context, id uint) (*localentities.Host, error)
	GetAllHosts(ctx context.Context) ([]localentities.Host, error)
	UpdateLastSeen(ctx context.Context, hostID uint) error
	// UpdateLastSeenAndAgentSession updates last_seen and agent_session_started_at (for node push heartbeats).
	UpdateLastSeenAndAgentSession(ctx context.Context, hostID uint, lastSeen time.Time, agentSessionStarted *time.Time) error
	// UpdateHostLabelsFromAgentPush updates name and/or ipv4 from cluster agent push (non-empty values only). Skips local collector id.
	UpdateHostLabelsFromAgentPush(ctx context.Context, hostID uint, name, ipv4 string) error
	// DeleteHostCascade removes a host row, node credentials, and all stored metrics scoped to that host_id.
	DeleteHostCascade(ctx context.Context, hostID uint) error
}

type hostRepository struct {
	db *gorm.DB
}

func NewHostRepository(db *gorm.DB) HostRepository {
	return &hostRepository{db: db}
}

// reclaimDuplicateLocalRows removes legacy host rows (same MAC or name, no push credential) so id=1 can be created.
func (r *hostRepository) reclaimDuplicateLocalRows(ctx context.Context, hostInfo localentities.HostInfo) error {
	for _, q := range []struct {
		col string
		val string
	}{
		{"mac_address", hostInfo.MacAddress},
		{"name", hostInfo.Name},
	} {
		if q.val == "" {
			continue
		}
		var h localentities.Host
		err := r.db.WithContext(ctx).
			Where(q.col+" = ? AND id != ?", q.val, localentities.LocalCollectorHostID).
			First(&h).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		var n int64
		if err := r.db.WithContext(ctx).Model(&nodeentities.NodeCredential{}).Where("host_id = ?", h.ID).Count(&n).Error; err != nil {
			return err
		}
		if n > 0 {
			continue
		}
		if err := r.DeleteHostCascade(ctx, h.ID); err != nil {
			return err
		}
	}
	return nil
}

func (r *hostRepository) UpsertLocalHost(ctx context.Context, hostInfo localentities.HostInfo) (*localentities.Host, error) {
	var host localentities.Host
	id := localentities.LocalCollectorHostID
	err := r.db.WithContext(ctx).Where("id = ?", id).First(&host).Error
	now := time.Now()
	if err == gorm.ErrRecordNotFound {
		if err := r.reclaimDuplicateLocalRows(ctx, hostInfo); err != nil {
			return nil, err
		}
		host = localentities.Host{
			ID:                   id,
			Name:                 hostInfo.Name,
			MacAddress:           hostInfo.MacAddress,
			IPv4:                 hostInfo.IPv4,
			OS:                   hostInfo.OS,
			Platform:             hostInfo.Platform,
			PlatformFamily:       hostInfo.PlatformFamily,
			PlatformVersion:      hostInfo.PlatformVersion,
			KernelVersion:        hostInfo.KernelVersion,
			VirtualizationSystem: hostInfo.VirtualizationSystem,
			VirtualizationRole:   hostInfo.VirtualizationRole,
			SystemHostID:         hostInfo.HostID,
			LastSeen:             now,
			CreatedAt:            now,
			UpdatedAt:            now,
		}
		return &host, r.db.WithContext(ctx).Create(&host).Error
	}
	if err != nil {
		return nil, err
	}
	host.Name = hostInfo.Name
	host.MacAddress = hostInfo.MacAddress
	host.IPv4 = hostInfo.IPv4
	host.OS = hostInfo.OS
	host.Platform = hostInfo.Platform
	host.PlatformFamily = hostInfo.PlatformFamily
	host.PlatformVersion = hostInfo.PlatformVersion
	host.KernelVersion = hostInfo.KernelVersion
	host.VirtualizationSystem = hostInfo.VirtualizationSystem
	host.VirtualizationRole = hostInfo.VirtualizationRole
	host.SystemHostID = hostInfo.HostID
	host.LastSeen = now
	host.UpdatedAt = now
	return &host, r.db.WithContext(ctx).Save(&host).Error
}

func (r *hostRepository) UpsertHost(ctx context.Context, hostInfo localentities.HostInfo) (*localentities.Host, error) {
	var host localentities.Host

	// 1) Try to find by MAC address (never match the reserved local collector row)
	err := r.db.WithContext(ctx).
		Where("mac_address = ? AND id != ?", hostInfo.MacAddress, localentities.LocalCollectorHostID).
		First(&host).Error
	if err == nil {
		// Found by MAC → update fields and timestamps
		now := time.Now()
		host.Name = hostInfo.Name
		host.IPv4 = hostInfo.IPv4
		host.OS = hostInfo.OS
		host.Platform = hostInfo.Platform
		host.PlatformFamily = hostInfo.PlatformFamily
		host.PlatformVersion = hostInfo.PlatformVersion
		host.KernelVersion = hostInfo.KernelVersion
		host.VirtualizationSystem = hostInfo.VirtualizationSystem
		host.VirtualizationRole = hostInfo.VirtualizationRole
		host.SystemHostID = hostInfo.HostID
		host.LastSeen = now
		host.UpdatedAt = now
		return &host, r.db.WithContext(ctx).Save(&host).Error
	}
	if err != gorm.ErrRecordNotFound {
		return nil, err
	}

	// 2) Not found by MAC → try to find by Name
	var hostByName localentities.Host
	err = r.db.WithContext(ctx).
		Where("name = ? AND id != ?", hostInfo.Name, localentities.LocalCollectorHostID).
		First(&hostByName).Error
	if err == nil {
		// Found by Name → update fields and timestamps
		now := time.Now()
		hostByName.MacAddress = hostInfo.MacAddress
		hostByName.IPv4 = hostInfo.IPv4
		hostByName.OS = hostInfo.OS
		hostByName.Platform = hostInfo.Platform
		hostByName.PlatformFamily = hostInfo.PlatformFamily
		hostByName.PlatformVersion = hostInfo.PlatformVersion
		hostByName.KernelVersion = hostInfo.KernelVersion
		hostByName.VirtualizationSystem = hostInfo.VirtualizationSystem
		hostByName.VirtualizationRole = hostInfo.VirtualizationRole
		hostByName.SystemHostID = hostInfo.HostID
		hostByName.LastSeen = now
		hostByName.UpdatedAt = now
		return &hostByName, r.db.WithContext(ctx).Save(&hostByName).Error
	}
	if err != gorm.ErrRecordNotFound {
		return nil, err
	}

	// 3) Not found by MAC or Name → create new record
	now := time.Now()
	host = localentities.Host{
		Name:                 hostInfo.Name,
		MacAddress:           hostInfo.MacAddress,
		IPv4:                 hostInfo.IPv4,
		OS:                   hostInfo.OS,
		Platform:             hostInfo.Platform,
		PlatformFamily:       hostInfo.PlatformFamily,
		PlatformVersion:      hostInfo.PlatformVersion,
		KernelVersion:        hostInfo.KernelVersion,
		VirtualizationSystem: hostInfo.VirtualizationSystem,
		VirtualizationRole:   hostInfo.VirtualizationRole,
		SystemHostID:         hostInfo.HostID,
		LastSeen:             now,
		CreatedAt:            now,
		UpdatedAt:            now,
	}
	return &host, r.db.WithContext(ctx).Create(&host).Error
}

func (r *hostRepository) GetHostByMacAddress(ctx context.Context, macAddress string) (*localentities.Host, error) {
	var host localentities.Host
	err := r.db.WithContext(ctx).Where("mac_address = ?", macAddress).First(&host).Error
	if err != nil {
		return nil, err
	}
	return &host, nil
}

func (r *hostRepository) GetHostByID(ctx context.Context, id uint) (*localentities.Host, error) {
	var host localentities.Host
	err := r.db.WithContext(ctx).First(&host, id).Error
	if err != nil {
		return nil, err
	}
	return &host, nil
}

func (r *hostRepository) GetAllHosts(ctx context.Context) ([]localentities.Host, error) {
	var hosts []localentities.Host
	// Local collector first, then others by id (stable UX).
	err := r.db.WithContext(ctx).
		Order(fmt.Sprintf("CASE WHEN id = %d THEN 0 ELSE 1 END, id ASC", localentities.LocalCollectorHostID)).
		Find(&hosts).Error
	return hosts, err
}

func (r *hostRepository) UpdateLastSeen(ctx context.Context, hostID uint) error {
	now := time.Now()
	return r.db.WithContext(ctx).Model(&localentities.Host{}).
		Where("id = ?", hostID).
		Updates(map[string]interface{}{"last_seen": now, "updated_at": now}).Error
}

func (r *hostRepository) UpdateLastSeenAndAgentSession(ctx context.Context, hostID uint, lastSeen time.Time, agentSessionStarted *time.Time) error {
	updates := map[string]interface{}{
		"last_seen":  lastSeen,
		"updated_at": lastSeen,
	}
	if agentSessionStarted != nil {
		updates["agent_session_started_at"] = *agentSessionStarted
	}
	return r.db.WithContext(ctx).Model(&localentities.Host{}).
		Where("id = ?", hostID).
		Updates(updates).Error
}

func (r *hostRepository) UpdateHostLabelsFromAgentPush(ctx context.Context, hostID uint, name, ipv4 string) error {
	if hostID == localentities.LocalCollectorHostID {
		return nil
	}
	updates := map[string]interface{}{}
	if n := strings.TrimSpace(name); n != "" {
		updates["name"] = n
	}
	if ip := strings.TrimSpace(ipv4); ip != "" {
		updates["ipv4"] = ip
	}
	if len(updates) == 0 {
		return nil
	}
	updates["updated_at"] = time.Now().UTC()
	return r.db.WithContext(ctx).Model(&localentities.Host{}).
		Where("id = ?", hostID).
		Updates(updates).Error
}

func (r *hostRepository) DeleteHostCascade(ctx context.Context, hostID uint) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Unscoped().Where("host_id = ?", hostID).Delete(&nodeentities.NodeCredential{}).Error; err != nil {
			return err
		}
		_ = tx.Model(&nodeentities.NodeJoinToken{}).Where("host_id = ?", hostID).Update("host_id", nil).Error

		if err := tx.Where("host_id = ?", hostID).Delete(&cpuentities.HistoricalCPUMetric{}).Error; err != nil {
			return err
		}
		if err := tx.Where("host_id = ?", hostID).Delete(&memoryentities.HistoricalMemoryMetric{}).Error; err != nil {
			return err
		}
		if err := tx.Where("host_id = ?", hostID).Delete(&diskentities.HistoricalDiskMetric{}).Error; err != nil {
			return err
		}
		if err := tx.Where("host_id = ?", hostID).Delete(&networkentities.HistoricalNetworkMetric{}).Error; err != nil {
			return err
		}

		sub := tx.Model(&dockerdomain.HistoricalDockerMetric{}).Select("timestamp").Where("host_id = ?", hostID)
		if err := tx.Where("metric_timestamp IN (?)", sub).Delete(&dockerentities.DockerContainerEntity{}).Error; err != nil {
			return err
		}
		if err := tx.Where("host_id = ?", hostID).Delete(&dockerdomain.HistoricalDockerMetric{}).Error; err != nil {
			return err
		}

		return tx.Unscoped().Where("id = ?", hostID).Delete(&localentities.Host{}).Error
	})
}
