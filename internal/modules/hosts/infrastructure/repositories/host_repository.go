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

	// 1) Try to find by MAC address
	err := r.db.WithContext(ctx).Where("mac_address = ?", hostInfo.MacAddress).First(&host).Error
	if err == nil {
		// Found by MAC → update name and timestamps
		now := time.Now()
		host.Name = hostInfo.Name
		host.LastSeen = now
		host.UpdatedAt = now
		return &host, r.db.WithContext(ctx).Save(&host).Error
	}
	if err != gorm.ErrRecordNotFound {
		return nil, err
	}

	// 2) Not found by MAC → try to find by Name
	var hostByName localentities.Host
	err = r.db.WithContext(ctx).Where("name = ?", hostInfo.Name).First(&hostByName).Error
	if err == nil {
		// Found by Name → update MAC and timestamps
		now := time.Now()
		hostByName.MacAddress = hostInfo.MacAddress
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
		Name:       hostInfo.Name,
		MacAddress: hostInfo.MacAddress,
		LastSeen:   now,
		CreatedAt:  now,
		UpdatedAt:  now,
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
	err := r.db.WithContext(ctx).Find(&hosts).Error
	return hosts, err
}
