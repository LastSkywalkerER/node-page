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
	List(ctx context.Context, offset, limit int) ([]*User, error)
	UpdateRole(ctx context.Context, userID uint, role string) error
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

// Delete deletes a user by ID
func (r *userRepository) Delete(ctx context.Context, userID uint) error {
	return r.db.WithContext(ctx).Delete(&User{}, userID).Error
}

// RefreshTokenRepository defines the interface for refresh token data operations
type RefreshTokenRepository interface {
	Create(ctx context.Context, token *RefreshToken) error
	FindByJTI(ctx context.Context, jti string) (*RefreshToken, error)
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

// RevokeAllByUserID revokes all refresh tokens for a user
func (r *refreshTokenRepository) RevokeAllByUserID(ctx context.Context, userID uint) error {
	now := time.Now()
	return r.db.WithContext(ctx).
		Model(&RefreshToken{}).
		Where("user_id = ? AND revoked_at IS NULL", userID).
		Update("revoked_at", now).Error
}

// DeleteExpired deletes expired refresh tokens
func (r *refreshTokenRepository) DeleteExpired(ctx context.Context) error {
	return r.db.WithContext(ctx).
		Where("expires_at < ?", time.Now()).
		Delete(&RefreshToken{}).Error
}
