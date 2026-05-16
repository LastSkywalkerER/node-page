package nodes

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
)

// JoinTokenRepository defines the interface for node join token operations.
type JoinTokenRepository interface {
	Create(ctx context.Context, t *NodeJoinToken) error
	FindByToken(ctx context.Context, token string) (*NodeJoinToken, error)
	MarkUsed(ctx context.Context, id uint, hostID uint) error
}

// CredentialRepository defines the interface for node credential operations.
type CredentialRepository interface {
	Create(ctx context.Context, c *NodeCredential) error
	SaveTokenHashForHost(ctx context.Context, hostID uint, tokenHash string) error
	FindByHostID(ctx context.Context, hostID uint) (*NodeCredential, error)
	FindByTokenHash(ctx context.Context, tokenHash string) (*NodeCredential, error)
	// CountWhereHostIDNot counts credentials for hosts other than excludeHostID (remote agents on this main).
	CountWhereHostIDNot(ctx context.Context, excludeHostID uint) (int64, error)
	// HostIDsWithPushCredential returns host IDs that have an active node push credential.
	HostIDsWithPushCredential(ctx context.Context) (map[uint]struct{}, error)
}

type nodeJoinTokenRepository struct {
	db *gorm.DB
}

// NewJoinTokenRepository creates a new node join token repository.
func NewJoinTokenRepository(db *gorm.DB) JoinTokenRepository {
	return &nodeJoinTokenRepository{db: db}
}

func (r *nodeJoinTokenRepository) Create(ctx context.Context, t *NodeJoinToken) error {
	return r.db.WithContext(ctx).Create(t).Error
}

func (r *nodeJoinTokenRepository) FindByToken(ctx context.Context, token string) (*NodeJoinToken, error) {
	var t NodeJoinToken
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
	return r.db.WithContext(ctx).Model(&NodeJoinToken{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{
			"used_at": now,
			"host_id": hostID,
		}).Error
}

type nodeCredentialRepository struct {
	db *gorm.DB
}

// NewCredentialRepository creates a new node credential repository.
func NewCredentialRepository(db *gorm.DB) CredentialRepository {
	return &nodeCredentialRepository{db: db}
}

func (r *nodeCredentialRepository) Create(ctx context.Context, c *NodeCredential) error {
	return r.db.WithContext(ctx).Create(c).Error
}

// SaveTokenHashForHost inserts or updates the push token hash for a host (join or admin regenerate).
func (r *nodeCredentialRepository) SaveTokenHashForHost(ctx context.Context, hostID uint, tokenHash string) error {
	db := r.db.WithContext(ctx)
	res := db.Unscoped().Model(&NodeCredential{}).
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
	return db.Create(&NodeCredential{
		HostID:    hostID,
		TokenHash: tokenHash,
	}).Error
}

func (r *nodeCredentialRepository) FindByHostID(ctx context.Context, hostID uint) (*NodeCredential, error) {
	var c NodeCredential
	err := r.db.WithContext(ctx).Where("host_id = ?", hostID).First(&c).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &c, nil
}

func (r *nodeCredentialRepository) FindByTokenHash(ctx context.Context, tokenHash string) (*NodeCredential, error) {
	var c NodeCredential
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
	err := r.db.WithContext(ctx).Model(&NodeCredential{}).
		Where("host_id <> ?", excludeHostID).
		Count(&n).Error
	return n, err
}

func (r *nodeCredentialRepository) HostIDsWithPushCredential(ctx context.Context) (map[uint]struct{}, error) {
	var ids []uint
	err := r.db.WithContext(ctx).Model(&NodeCredential{}).Distinct().Pluck("host_id", &ids).Error
	if err != nil {
		return nil, err
	}
	m := make(map[uint]struct{}, len(ids))
	for _, id := range ids {
		m[id] = struct{}{}
	}
	return m, nil
}
