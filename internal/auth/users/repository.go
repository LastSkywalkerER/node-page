package users

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
)

// UserRepository defines the interface for user data operations
type UserRepository interface {
	Create(ctx context.Context, user *User) error
	FindByEmail(ctx context.Context, email string) (*User, error)
	FindByID(ctx context.Context, id uint) (*User, error)
	Count(ctx context.Context) (int64, error)
	CountByRole(ctx context.Context, role string) (int64, error)
	List(ctx context.Context, offset, limit int) ([]*User, error)
	UpdateRole(ctx context.Context, userID uint, role string) error
	UpdatePassword(ctx context.Context, userID uint, passwordHash string) error
	Delete(ctx context.Context, userID uint) error
}

// userRepository implements UserRepository interface
type userRepository struct {
	db *gorm.DB
}

// NewUserRepository creates a new user repository
func NewUserRepository(db *gorm.DB) UserRepository {
	return &userRepository{db: db}
}

// Create creates a new user in the database
func (r *userRepository) Create(ctx context.Context, user *User) error {
	return r.db.Create(user).Error
}

// FindByEmail finds a user by email address
func (r *userRepository) FindByEmail(ctx context.Context, email string) (*User, error) {
	var user User
	err := r.db.WithContext(ctx).Where("email = ?", email).First(&user).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &user, nil
}

// FindByID finds a user by ID
func (r *userRepository) FindByID(ctx context.Context, id uint) (*User, error) {
	var user User
	err := r.db.WithContext(ctx).First(&user, id).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &user, nil
}

// Count returns the total number of users
func (r *userRepository) Count(ctx context.Context) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&User{}).Count(&count).Error
	return count, err
}

// CountByRole returns the number of users with the given role.
func (r *userRepository) CountByRole(ctx context.Context, role string) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&User{}).Where("role = ?", role).Count(&count).Error
	return count, err
}

// List returns a paginated list of users
func (r *userRepository) List(ctx context.Context, offset, limit int) ([]*User, error) {
	var users []*User
	err := r.db.WithContext(ctx).Offset(offset).Limit(limit).Find(&users).Error
	return users, err
}

// UpdateRole updates a user's role
func (r *userRepository) UpdateRole(ctx context.Context, userID uint, role string) error {
	return r.db.WithContext(ctx).Model(&User{}).Where("id = ?", userID).Update("role", role).Error
}

// UpdatePassword updates a user's bcrypt password hash
func (r *userRepository) UpdatePassword(ctx context.Context, userID uint, passwordHash string) error {
	return r.db.WithContext(ctx).Model(&User{}).Where("id = ?", userID).Update("password_hash", passwordHash).Error
}

// Delete deletes a user by ID
func (r *userRepository) Delete(ctx context.Context, userID uint) error {
	return r.db.WithContext(ctx).Delete(&User{}, userID).Error
}

// RefreshTokenRepository defines the interface for refresh token data operations
type RefreshTokenRepository interface {
	Create(ctx context.Context, token *RefreshToken) error
	FindByJTI(ctx context.Context, jti string) (*RefreshToken, error)
	RevokeByJTI(ctx context.Context, jti string) error
	ConsumeByJTI(ctx context.Context, jti string) (bool, error)
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
func (r *refreshTokenRepository) Create(ctx context.Context, token *RefreshToken) error {
	return r.db.WithContext(ctx).Create(token).Error
}

// FindByJTI finds a refresh token by JTI (JWT ID)
func (r *refreshTokenRepository) FindByJTI(ctx context.Context, jti string) (*RefreshToken, error) {
	var token RefreshToken
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
		Model(&RefreshToken{}).
		Where("jti = ?", jti).
		Update("revoked_at", now).Error
}

// ConsumeByJTI atomically marks a refresh token as revoked.
// Returns true if this call revoked the token (winner of any concurrent race),
// false if it was already revoked / missing (replay or concurrent loser).
func (r *refreshTokenRepository) ConsumeByJTI(ctx context.Context, jti string) (bool, error) {
	now := time.Now()
	res := r.db.WithContext(ctx).
		Model(&RefreshToken{}).
		Where("jti = ? AND revoked_at IS NULL", jti).
		Update("revoked_at", now)
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected == 1, nil
}

// RevokeAllByUserID revokes all refresh tokens for a user
func (r *refreshTokenRepository) RevokeAllByUserID(ctx context.Context, userID uint) error {
	now := time.Now()
	return r.db.WithContext(ctx).
		Model(&RefreshToken{}).
		Where("user_id = ? AND revoked_at IS NULL", userID).
		Update("revoked_at", now).Error
}

// refreshRevokeRetention is how long a revoked refresh token row is kept before
// it is hard-deleted. It must comfortably exceed refreshGraceWindow (the brief
// window where a just-revoked token is still honored for the multi-tab rotation
// race); after that the row is useless — a missing JTI is rejected exactly like
// a revoked one, so deleting it opens no replay hole.
const refreshRevokeRetention = time.Hour

// DeleteExpired hard-deletes refresh tokens that can never be used again: those
// past their expiry, AND those revoked longer than refreshRevokeRetention ago.
// The second clause is what actually bounds the table — every refresh rotates
// the token (revoking the old one), so without it revoked-but-unexpired rows
// would accumulate for the full refresh TTL (default 90 days). Idempotent; safe
// to call periodically.
func (r *refreshTokenRepository) DeleteExpired(ctx context.Context) error {
	now := time.Now()
	// Unscoped(): RefreshToken has a gorm.DeletedAt, so a plain Delete would only
	// SOFT-delete (set deleted_at) and leave the row — exactly the bloat we are
	// trying to reclaim. Hard-delete so the rows actually leave the table.
	return r.db.WithContext(ctx).Unscoped().
		Where("expires_at < ? OR (revoked_at IS NOT NULL AND revoked_at < ?)", now, now.Add(-refreshRevokeRetention)).
		Delete(&RefreshToken{}).Error
}
