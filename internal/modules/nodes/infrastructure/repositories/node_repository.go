package repositories

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"

	nodeentities "system-stats/internal/modules/nodes/infrastructure/entities"
)

// NodeJoinTokenRepository defines the interface for node join token operations.
type NodeJoinTokenRepository interface {
	Create(ctx context.Context, t *nodeentities.NodeJoinToken) error
	FindByToken(ctx context.Context, token string) (*nodeentities.NodeJoinToken, error)
	MarkUsed(ctx context.Context, id uint, hostID uint) error
}

// NodeCredentialRepository defines the interface for node credential operations.
type NodeCredentialRepository interface {
	Create(ctx context.Context, c *nodeentities.NodeCredential) error
	SaveTokenHashForHost(ctx context.Context, hostID uint, tokenHash string) error
	FindByHostID(ctx context.Context, hostID uint) (*nodeentities.NodeCredential, error)
	FindByTokenHash(ctx context.Context, tokenHash string) (*nodeentities.NodeCredential, error)
	// CountWhereHostIDNot counts credentials for hosts other than excludeHostID (remote agents on this main).
	CountWhereHostIDNot(ctx context.Context, excludeHostID uint) (int64, error)
	// HostIDsWithPushCredential returns host IDs that have an active node push credential.
	HostIDsWithPushCredential(ctx context.Context) (map[uint]struct{}, error)
}

type nodeJoinTokenRepository struct {
	db *gorm.DB
}

// NewNodeJoinTokenRepository creates a new node join token repository.
func NewNodeJoinTokenRepository(db *gorm.DB) NodeJoinTokenRepository {
	return &nodeJoinTokenRepository{db: db}
}

func (r *nodeJoinTokenRepository) Create(ctx context.Context, t *nodeentities.NodeJoinToken) error {
	return r.db.WithContext(ctx).Create(t).Error
}

func (r *nodeJoinTokenRepository) FindByToken(ctx context.Context, token string) (*nodeentities.NodeJoinToken, error) {
	var t nodeentities.NodeJoinToken
	err := r.db.WithContext(ctx).
		Where("token = ? AND used_at IS NULL AND expires_at > ?", token, time.Now().UTC()).
		First(&t).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &t, nil
}

func (r *nodeJoinTokenRepository) MarkUsed(ctx context.Context, id uint, hostID uint) error {
	now := time.Now().UTC()
	return r.db.WithContext(ctx).Model(&nodeentities.NodeJoinToken{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{
			"used_at": now,
			"host_id": hostID,
		}).Error
}

type nodeCredentialRepository struct {
	db *gorm.DB
}

// NewNodeCredentialRepository creates a new node credential repository.
func NewNodeCredentialRepository(db *gorm.DB) NodeCredentialRepository {
	return &nodeCredentialRepository{db: db}
}

func (r *nodeCredentialRepository) Create(ctx context.Context, c *nodeentities.NodeCredential) error {
	return r.db.WithContext(ctx).Create(c).Error
}

// SaveTokenHashForHost inserts or updates the push token hash for a host (join or admin regenerate).
func (r *nodeCredentialRepository) SaveTokenHashForHost(ctx context.Context, hostID uint, tokenHash string) error {
	db := r.db.WithContext(ctx)
	res := db.Unscoped().Model(&nodeentities.NodeCredential{}).
		Where("host_id = ?", hostID).
		Updates(map[string]interface{}{
			"token_hash": tokenHash,
			"deleted_at": nil,
		})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected > 0 {
		return nil
	}
	return db.Create(&nodeentities.NodeCredential{
		HostID:    hostID,
		TokenHash: tokenHash,
	}).Error
}

func (r *nodeCredentialRepository) FindByHostID(ctx context.Context, hostID uint) (*nodeentities.NodeCredential, error) {
	var c nodeentities.NodeCredential
	err := r.db.WithContext(ctx).Where("host_id = ?", hostID).First(&c).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &c, nil
}

func (r *nodeCredentialRepository) FindByTokenHash(ctx context.Context, tokenHash string) (*nodeentities.NodeCredential, error) {
	var c nodeentities.NodeCredential
	err := r.db.WithContext(ctx).Where("token_hash = ?", tokenHash).First(&c).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &c, nil
}

func (r *nodeCredentialRepository) CountWhereHostIDNot(ctx context.Context, excludeHostID uint) (int64, error) {
	var n int64
	err := r.db.WithContext(ctx).Model(&nodeentities.NodeCredential{}).
		Where("host_id <> ?", excludeHostID).
		Count(&n).Error
	return n, err
}

func (r *nodeCredentialRepository) HostIDsWithPushCredential(ctx context.Context) (map[uint]struct{}, error) {
	var ids []uint
	err := r.db.WithContext(ctx).Model(&nodeentities.NodeCredential{}).Distinct().Pluck("host_id", &ids).Error
	if err != nil {
		return nil, err
	}
	m := make(map[uint]struct{}, len(ids))
	for _, id := range ids {
		m[id] = struct{}{}
	}
	return m, nil
}
