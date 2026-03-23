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

	invservice "system-stats/internal/modules/invitations/application"
)

// UserService handles user-related business logic
type UserService interface {
	Register(ctx context.Context, email, password string, inviteToken *string) (*localentities.User, error)
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
	userRepo    userrepos.UserRepository
	tokenSvc    TokenService
	invService  invservice.Service
	validator   *validator.Validate
}

// NewUserService creates a new user service
func NewUserService(
	userRepo userrepos.UserRepository,
	tokenSvc TokenService,
	invService invservice.Service,
) UserService {
	return &userService{
		userRepo:   userRepo,
		tokenSvc:   tokenSvc,
		invService: invService,
		validator:  validator.New(),
	}
}

// Register creates a new user account.
// If inviteToken is provided and valid, bypasses "users exist" check and creates user with role USER.
func (s *userService) Register(ctx context.Context, email, password string, inviteToken *string) (*localentities.User, error) {
	var invID uint
	var role string = "ADMIN"

	if inviteToken != nil && *inviteToken != "" {
		inv, err := s.invService.ValidateToken(ctx, *inviteToken)
		if err != nil {
			return nil, fmt.Errorf("invalid invitation: %w", err)
		}
		invitedEmail := strings.ToLower(strings.TrimSpace(inv.Email))
		userEmail := strings.ToLower(strings.TrimSpace(email))
		if invitedEmail != userEmail {
			return nil, fmt.Errorf("invitation email mismatch: invitation is for %s", inv.Email)
		}
		invID = inv.ID
		role = "USER"
	} else {
		// No invite: registration only allowed when no users exist
		count, err := s.Count(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to check user count: %w", err)
		}
		if count > 0 {
			return nil, errors.New("registration is disabled: users already exist")
		}
	}

	hashedPassword, err := s.HashPassword(password)
	if err != nil {
		return nil, fmt.Errorf("failed to hash password: %w", err)
	}

	user := &localentities.User{
		Email:        strings.ToLower(strings.TrimSpace(email)),
		PasswordHash: hashedPassword,
		Role:         role,
	}

	if err := s.userRepo.Create(ctx, user); err != nil {
		return nil, fmt.Errorf("failed to create user: %w", err)
	}

	if invID != 0 {
		if err := s.invService.Consume(ctx, invID, user.ID); err != nil {
			return nil, fmt.Errorf("failed to consume invitation: %w", err)
		}
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
