package repositories

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"

	localentities "system-stats/internal/modules/users/infrastructure/entities"
)

// RefreshTokenRepository defines the interface for refresh token data operations
type RefreshTokenRepository interface {
	Create(ctx context.Context, token *localentities.RefreshToken) error
	FindByJTI(ctx context.Context, jti string) (*localentities.RefreshToken, error)
	RevokeByJTI(ctx context.Context, jti string) error
	RevokeAllByUserID(ctx context.Context, userID uint) error
	DeleteExpired(ctx context.Context) error
}

// refreshTokenRepository implements RefreshTokenRepository interface
type refreshTokenRepository struct {
	db *gorm.DB
}

// NewRefreshTokenRepository creates a new refresh token repository
func NewRefreshTokenRepository(db *gorm.DB) RefreshTokenRepository {
	return &refreshTokenRepository{db: db}
}

// Create creates a new refresh token in the database
func (r *refreshTokenRepository) Create(ctx context.Context, token *localentities.RefreshToken) error {
	return r.db.WithContext(ctx).Create(token).Error
}

// FindByJTI finds a refresh token by JTI (JWT ID)
func (r *refreshTokenRepository) FindByJTI(ctx context.Context, jti string) (*localentities.RefreshToken, error) {
	var token localentities.RefreshToken
	err := r.db.WithContext(ctx).
		Preload("User").
		Where("jti = ? AND expires_at > ? AND revoked_at IS NULL", jti, time.Now()).
		First(&token).Error

	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &token, nil
}

// RevokeByJTI revokes a refresh token by JTI
func (r *refreshTokenRepository) RevokeByJTI(ctx context.Context, jti string) error {
	now := time.Now()
	return r.db.WithContext(ctx).
		Model(&localentities.RefreshToken{}).
		Where("jti = ?", jti).
		Update("revoked_at", now).Error
}

// RevokeAllByUserID revokes all refresh tokens for a user
func (r *refreshTokenRepository) RevokeAllByUserID(ctx context.Context, userID uint) error {
	now := time.Now()
	return r.db.WithContext(ctx).
		Model(&localentities.RefreshToken{}).
		Where("user_id = ? AND revoked_at IS NULL", userID).
		Update("revoked_at", now).Error
}

// DeleteExpired deletes expired refresh tokens
func (r *refreshTokenRepository) DeleteExpired(ctx context.Context) error {
	return r.db.WithContext(ctx).
		Where("expires_at < ?", time.Now()).
		Delete(&localentities.RefreshToken{}).Error
}

