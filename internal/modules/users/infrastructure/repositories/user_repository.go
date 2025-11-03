package repositories

import (
	"context"
	"errors"

	"gorm.io/gorm"

	localentities "system-stats/internal/modules/users/infrastructure/entities"
)

// UserRepository defines the interface for user data operations
type UserRepository interface {
	Create(ctx context.Context, user *localentities.User) error
	FindByEmail(ctx context.Context, email string) (*localentities.User, error)
	FindByID(ctx context.Context, id uint) (*localentities.User, error)
	Count(ctx context.Context) (int64, error)
	List(ctx context.Context, offset, limit int) ([]*localentities.User, error)
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
func (r *userRepository) Create(ctx context.Context, user *localentities.User) error {
	return r.db.Create(user).Error
}

// FindByEmail finds a user by email address
func (r *userRepository) FindByEmail(ctx context.Context, email string) (*localentities.User, error) {
	var user localentities.User
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
func (r *userRepository) FindByID(ctx context.Context, id uint) (*localentities.User, error) {
	var user localentities.User
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
	err := r.db.WithContext(ctx).Model(&localentities.User{}).Count(&count).Error
	return count, err
}

// List returns a paginated list of users
func (r *userRepository) List(ctx context.Context, offset, limit int) ([]*localentities.User, error) {
	var users []*localentities.User
	err := r.db.WithContext(ctx).Offset(offset).Limit(limit).Find(&users).Error
	return users, err
}

// UpdateRole updates a user's role
func (r *userRepository) UpdateRole(ctx context.Context, userID uint, role string) error {
	return r.db.WithContext(ctx).Model(&localentities.User{}).Where("id = ?", userID).Update("role", role).Error
}

// Delete deletes a user by ID
func (r *userRepository) Delete(ctx context.Context, userID uint) error {
	return r.db.WithContext(ctx).Delete(&localentities.User{}, userID).Error
}
