package application

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/go-playground/validator/v10"
	"golang.org/x/crypto/bcrypt"

	localentities "system-stats/internal/modules/users/infrastructure/entities"
	userrepos "system-stats/internal/modules/users/infrastructure/repositories"
)

// UserService handles user-related business logic
type UserService interface {
	Register(ctx context.Context, email, password string) (*localentities.User, error)
	Login(ctx context.Context, email, password string) (*localentities.User, error)
	GetByID(ctx context.Context, id uint) (*localentities.User, error)
	GetByEmail(ctx context.Context, email string) (*localentities.User, error)
	List(ctx context.Context, offset, limit int) ([]*localentities.User, error)
	UpdateRole(ctx context.Context, userID uint, role string) error
	Delete(ctx context.Context, userID uint) error
	Count(ctx context.Context) (int64, error)
	HashPassword(password string) (string, error)
	VerifyPassword(hash, password string) error
}

type userService struct {
	userRepo  userrepos.UserRepository
	tokenSvc  TokenService
	validator *validator.Validate
}

// NewUserService creates a new user service
func NewUserService(
	userRepo userrepos.UserRepository,
	tokenSvc TokenService,
) UserService {
	return &userService{
		userRepo:  userRepo,
		tokenSvc:  tokenSvc,
		validator: validator.New(),
	}
}

// Register creates a new user account
func (s *userService) Register(ctx context.Context, email, password string) (*localentities.User, error) {
	// Check if there are already users in the database
	count, err := s.Count(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to check user count: %w", err)
	}
	if count > 0 {
		return nil, errors.New("registration is disabled: users already exist")
	}

	// Create user with minimal validation and proper password hashing
	hashedPassword, err := s.HashPassword(password)
	if err != nil {
		return nil, fmt.Errorf("failed to hash password: %w", err)
	}

	user := &localentities.User{
		Email:        strings.ToLower(strings.TrimSpace(email)),
		PasswordHash: hashedPassword,
		Role:         "ADMIN",
	}

	if err := s.userRepo.Create(ctx, user); err != nil {
		return nil, fmt.Errorf("failed to create user: %w", err)
	}

	return user, nil
}

// Login authenticates a user
func (s *userService) Login(ctx context.Context, email, password string) (*localentities.User, error) {
	// Normalize email
	email = strings.ToLower(strings.TrimSpace(email))

	// Find user
	user, err := s.userRepo.FindByEmail(ctx, email)
	if err != nil {
		return nil, fmt.Errorf("failed to find user: %w", err)
	}
	if user == nil {
		return nil, errors.New("invalid credentials")
	}

	// Verify password
	if err := s.VerifyPassword(user.PasswordHash, password); err != nil {
		return nil, errors.New("invalid credentials")
	}

	return user, nil
}

// GetByID retrieves a user by ID
func (s *userService) GetByID(ctx context.Context, id uint) (*localentities.User, error) {
	return s.userRepo.FindByID(ctx, id)
}

// GetByEmail retrieves a user by email
func (s *userService) GetByEmail(ctx context.Context, email string) (*localentities.User, error) {
	return s.userRepo.FindByEmail(ctx, strings.ToLower(strings.TrimSpace(email)))
}

// List retrieves a paginated list of users
func (s *userService) List(ctx context.Context, offset, limit int) ([]*localentities.User, error) {
	return s.userRepo.List(ctx, offset, limit)
}

// UpdateRole updates a user's role
func (s *userService) UpdateRole(ctx context.Context, userID uint, role string) error {
	if role != "ADMIN" && role != "USER" {
		return errors.New("invalid role")
	}
	return s.userRepo.UpdateRole(ctx, userID, role)
}

// Delete deletes a user
func (s *userService) Delete(ctx context.Context, userID uint) error {
	return s.userRepo.Delete(ctx, userID)
}

// Count returns the total number of users
func (s *userService) Count(ctx context.Context) (int64, error) {
	return s.userRepo.Count(ctx)
}

// HashPassword creates a bcrypt hash of the password
func (s *userService) HashPassword(password string) (string, error) {
	bytes, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(bytes), err
}

// VerifyPassword verifies a password against its hash
func (s *userService) VerifyPassword(hash, password string) error {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
}
