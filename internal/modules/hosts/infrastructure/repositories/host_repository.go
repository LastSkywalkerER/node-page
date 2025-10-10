package repositories

import (
	"context"
	"time"

	"gorm.io/gorm"

	localentities "system-stats/internal/modules/hosts/infrastructure/entities"
)

type HostRepository interface {
	UpsertHost(ctx context.Context, hostInfo localentities.HostInfo) (*localentities.Host, error)
	GetHostByMacAddress(ctx context.Context, macAddress string) (*localentities.Host, error)
	GetHostByID(ctx context.Context, id uint) (*localentities.Host, error)
	GetAllHosts(ctx context.Context) ([]localentities.Host, error)
}

type hostRepository struct {
	db *gorm.DB
}

func NewHostRepository(db *gorm.DB) HostRepository {
	// Auto-migrate the hosts table
	db.AutoMigrate(&localentities.Host{})
	return &hostRepository{db: db}
}

func (r *hostRepository) UpsertHost(ctx context.Context, hostInfo localentities.HostInfo) (*localentities.Host, error) {
	var host localentities.Host

	// Try to find existing host by MAC address
	err := r.db.WithContext(ctx).Where("mac_address = ?", hostInfo.MacAddress).First(&host).Error

	if err != nil && err != gorm.ErrRecordNotFound {
		return nil, err
	}

	if err == gorm.ErrRecordNotFound {
		// Create new host record
		now := time.Now()
		host = localentities.Host{
			Name:       hostInfo.Name,
			MacAddress: hostInfo.MacAddress,
			LastSeen:   now,
			CreatedAt:  now,
			UpdatedAt:  now,
		}
		return &host, r.db.WithContext(ctx).Create(&host).Error
	} else {
		// Update existing host (trust MAC address, update name if changed)
		now := time.Now()
		host.Name = hostInfo.Name
		host.LastSeen = now
		host.UpdatedAt = now
		return &host, r.db.WithContext(ctx).Save(&host).Error
	}
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
	err := r.db.WithContext(ctx).Find(&hosts).Error
	return hosts, err
}
