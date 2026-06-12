package users

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/go-playground/validator/v10"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	invitations "system-stats/internal/auth/invitations"
)

var (
	ErrAuthSecretsNotConfigured = errors.New("authentication secrets are not configured")
	ErrRegistrationDisabled     = errors.New("registration is disabled")
	ErrEmailExists              = errors.New("email already exists")
	ErrInvalidCredentials       = errors.New("invalid credentials")
	ErrInvalidInvitation        = errors.New("invalid invitation")
	ErrInvitationEmailMismatch  = errors.New("invitation email mismatch")
	ErrCannotDemoteSelf         = errors.New("cannot demote yourself")
	ErrCannotDemoteLastAdmin    = errors.New("cannot demote the last admin")
	ErrCannotDeleteLastAdmin    = errors.New("cannot delete the last admin")
)

// TokenPair represents access and refresh tokens
type TokenPair struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	ExpiresIn    int64     `json:"expires_in"` // seconds for access token
	AccessExp    time.Time `json:"-"`
	RefreshExp   time.Time `json:"-"`
}

// Claims represents JWT claims
type Claims struct {
	UserID uint   `json:"sub"`
	Email  string `json:"email"`
	Role   string `json:"role"`
	JTI    string `json:"jti"`
	jwt.RegisteredClaims
}

// UserService handles user-related business logic
type UserService interface {
	Register(ctx context.Context, email, password string, inviteToken *string) (*User, error)
	Login(ctx context.Context, email, password string) (*User, error)
	GetByID(ctx context.Context, id uint) (*User, error)
	GetByEmail(ctx context.Context, email string) (*User, error)
	List(ctx context.Context, offset, limit int) ([]*User, error)
	UpdateRole(ctx context.Context, currentUserID, targetUserID uint, role string) error
	Delete(ctx context.Context, currentUserID, targetUserID uint) error
	Count(ctx context.Context) (int64, error)
	HashPassword(password string) (string, error)
	VerifyPassword(hash, password string) error
}

// RaftReplicator is the subset of internal/cluster/raft.Service the user
// service needs. Defined locally to avoid import cycles.
type RaftReplicator interface {
	Enabled() bool
	SubmitUserUpsert(ctx context.Context, email, passwordHash, role string) error
}

type userService struct {
	userRepo   UserRepository
	tokenSvc   TokenService
	invService invitations.Service
	validator  *validator.Validate
	raft       RaftReplicator
}

// NewUserService creates a new user service
func NewUserService(
	userRepo UserRepository,
	tokenSvc TokenService,
	invService invitations.Service,
) UserService {
	return &userService{
		userRepo:   userRepo,
		tokenSvc:   tokenSvc,
		invService: invService,
		validator:  validator.New(),
	}
}

// AttachRaftReplicator wires a Raft replicator into an existing UserService
// instance so that user creation also publishes a CmdUserUpsert. Idempotent
// and safe to call after the service has been handed to other components.
func AttachRaftReplicator(svc UserService, r RaftReplicator) {
	if impl, ok := svc.(*userService); ok {
		impl.raft = r
	}
}

// Register creates a new user account.
// If inviteToken is provided and valid, bypasses "users exist" check and creates user with role USER.
func (s *userService) Register(ctx context.Context, email, password string, inviteToken *string) (*User, error) {
	var invID uint
	var role string = "ADMIN"

	if inviteToken != nil && *inviteToken != "" {
		inv, err := s.invService.ValidateToken(ctx, *inviteToken)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrInvalidInvitation, err)
		}
		invitedEmail := strings.ToLower(strings.TrimSpace(inv.Email))
		userEmail := strings.ToLower(strings.TrimSpace(email))
		if invitedEmail != userEmail {
			return nil, fmt.Errorf("%w: invitation is for %s", ErrInvitationEmailMismatch, inv.Email)
		}
		invID = inv.ID
		role = "USER"
	} else {
		count, err := s.Count(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to check user count: %w", err)
		}
		if count > 0 {
			return nil, fmt.Errorf("%w: users already exist", ErrRegistrationDisabled)
		}
	}

	hashedPassword, err := s.HashPassword(password)
	if err != nil {
		return nil, fmt.Errorf("failed to hash password: %w", err)
	}

	user := &User{
		Email:        strings.ToLower(strings.TrimSpace(email)),
		PasswordHash: hashedPassword,
		Role:         role,
	}

	if err := s.userRepo.Create(ctx, user); err != nil {
		if strings.Contains(err.Error(), "UNIQUE") || strings.Contains(err.Error(), "duplicate") {
			return nil, fmt.Errorf("%w: %w", ErrEmailExists, err)
		}
		return nil, fmt.Errorf("failed to create user: %w", err)
	}

	if invID != 0 {
		if err := s.invService.Consume(ctx, invID, user.ID); err != nil {
			return nil, fmt.Errorf("failed to consume invitation: %w", err)
		}
	}

	// Replicate user across the cluster so a session minted on any node
	// authenticates the same user everywhere. A fresh single-voter
	// cluster may still be in the Candidate state at this point
	// (election takes a few hundred ms after BootstrapCluster). Retry
	// on ErrNotLeader for a few seconds — without this, the user
	// ends up in the leader's local SQLite only, never gets to the
	// Raft log, and any joining node thinks setup_needed=true forever.
	if s.raft != nil && s.raft.Enabled() {
		deadline := time.Now().Add(5 * time.Second)
		var lastErr error
		for time.Now().Before(deadline) {
			err := s.raft.SubmitUserUpsert(ctx, user.Email, user.PasswordHash, user.Role)
			if err == nil {
				lastErr = nil
				break
			}
			lastErr = err
			// ErrNotLeader / ErrDisabled is exposed via error.Error()
			// here because the users package can't depend on the raft
			// package. The string match is intentionally narrow.
			msg := err.Error()
			if strings.Contains(msg, "not the leader") || strings.Contains(msg, "raft: disabled") {
				time.Sleep(200 * time.Millisecond)
				continue
			}
			// Non-transient error — stop retrying.
			break
		}
		if lastErr != nil {
			// User exists locally; cluster will need a backfill once
			// leadership stabilises. Don't fail the wizard but make
			// the failure loud in logs.
			fmt.Printf("users.Register: raft replication of %q failed after retry: %v\n", user.Email, lastErr)
		}
	}

	return user, nil
}

// Login authenticates a user
func (s *userService) Login(ctx context.Context, email, password string) (*User, error) {
	// Normalize email
	email = strings.ToLower(strings.TrimSpace(email))

	// Find user
	user, err := s.userRepo.FindByEmail(ctx, email)
	if err != nil {
		return nil, fmt.Errorf("failed to find user: %w", err)
	}
	if user == nil {
		return nil, ErrInvalidCredentials
	}

	if err := s.VerifyPassword(user.PasswordHash, password); err != nil {
		return nil, ErrInvalidCredentials
	}

	return user, nil
}

// GetByID retrieves a user by ID
func (s *userService) GetByID(ctx context.Context, id uint) (*User, error) {
	return s.userRepo.FindByID(ctx, id)
}

// GetByEmail retrieves a user by email
func (s *userService) GetByEmail(ctx context.Context, email string) (*User, error) {
	return s.userRepo.FindByEmail(ctx, strings.ToLower(strings.TrimSpace(email)))
}

// List retrieves a paginated list of users
func (s *userService) List(ctx context.Context, offset, limit int) ([]*User, error) {
	return s.userRepo.List(ctx, offset, limit)
}

// UpdateRole updates a user's role.
// Guards: forbids self-demotion (admin cannot demote themselves) and demoting the last admin.
func (s *userService) UpdateRole(ctx context.Context, currentUserID, targetUserID uint, role string) error {
	if role != "ADMIN" && role != "USER" {
		return errors.New("invalid role")
	}
	if role == "USER" {
		if currentUserID == targetUserID {
			return ErrCannotDemoteSelf
		}
		target, err := s.userRepo.FindByID(ctx, targetUserID)
		if err != nil {
			return err
		}
		if target != nil && target.Role == "ADMIN" {
			admins, err := s.userRepo.CountByRole(ctx, "ADMIN")
			if err != nil {
				return err
			}
			if admins <= 1 {
				return ErrCannotDemoteLastAdmin
			}
		}
	}
	return s.userRepo.UpdateRole(ctx, targetUserID, role)
}

// Delete deletes a user and revokes their refresh tokens.
// Guards: forbids deleting the last admin. Self-deletion is blocked at the handler layer.
func (s *userService) Delete(ctx context.Context, currentUserID, targetUserID uint) error {
	target, err := s.userRepo.FindByID(ctx, targetUserID)
	if err != nil {
		return err
	}
	if target != nil && target.Role == "ADMIN" {
		admins, err := s.userRepo.CountByRole(ctx, "ADMIN")
		if err != nil {
			return err
		}
		if admins <= 1 {
			return ErrCannotDeleteLastAdmin
		}
	}
	if err := s.userRepo.Delete(ctx, targetUserID); err != nil {
		return err
	}
	// Best-effort revoke; user is already soft-deleted, tokens will expire by exp.
	if s.tokenSvc != nil {
		if rerr := s.tokenSvc.RevokeAllUserTokens(ctx, targetUserID); rerr != nil {
			// log via gorm context; caller will not see this — intentional
			_ = rerr
		}
	}
	return nil
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

// TokenService handles JWT token operations
type TokenService interface {
	GenerateTokens(ctx context.Context, user *User) (*TokenPair, error)
	ValidateAccessToken(tokenString string) (*Claims, error)
	ValidateRefreshToken(ctx context.Context, tokenString string) (*RefreshToken, error)
	HashRefreshToken(token string) (string, error)
	PersistRefreshToken(ctx context.Context, userID uint, jti, tokenHash string, expiresAt time.Time) error
	RevokeRefreshToken(ctx context.Context, jti string) error
	ConsumeRefreshToken(ctx context.Context, jti string) (bool, error)
	RevokeAllUserTokens(ctx context.Context, userID uint) error
	// SetSecrets atomically replaces the JWT signing keys, e.g. after a
	// joining node receives the cluster-shared secrets via the Raft FSM.
	// Tokens minted before the swap will fail validation; callers should
	// only swap when no live sessions are expected (joining flow).
	SetSecrets(accessSecret, refreshSecret string)
	// AccessTokenTTL / RefreshTokenTTL expose the configured lifetimes so
	// cookie Max-Age always matches the tokens instead of hardcoded values.
	AccessTokenTTL() time.Duration
	RefreshTokenTTL() time.Duration
}

type tokenService struct {
	refreshTokenRepo RefreshTokenRepository
	secretsMu        sync.RWMutex
	accessSecret     []byte
	refreshSecret    []byte
	accessTTL        time.Duration
	refreshTTL       time.Duration
}

// NewTokenService creates a new token service
func NewTokenService(
	refreshTokenRepo RefreshTokenRepository,
	accessSecret, refreshSecret string,
	accessTTL, refreshTTL time.Duration,
) TokenService {
	return &tokenService{
		refreshTokenRepo: refreshTokenRepo,
		accessSecret:     []byte(accessSecret),
		refreshSecret:    []byte(refreshSecret),
		accessTTL:        accessTTL,
		refreshTTL:       refreshTTL,
	}
}

// SetSecrets atomically replaces both signing keys.
func (s *tokenService) SetSecrets(accessSecret, refreshSecret string) {
	s.secretsMu.Lock()
	defer s.secretsMu.Unlock()
	s.accessSecret = []byte(accessSecret)
	s.refreshSecret = []byte(refreshSecret)
}

func (s *tokenService) loadSecrets() ([]byte, []byte) {
	s.secretsMu.RLock()
	defer s.secretsMu.RUnlock()
	return s.accessSecret, s.refreshSecret
}

// GenerateTokens generates a new pair of access and refresh tokens
func (s *tokenService) GenerateTokens(ctx context.Context, user *User) (*TokenPair, error) {
	accessSecret, refreshSecret := s.loadSecrets()
	if len(accessSecret) == 0 || len(refreshSecret) == 0 {
		return nil, fmt.Errorf("%w", ErrAuthSecretsNotConfigured)
	}
	now := time.Now()
	accessExp := now.Add(s.accessTTL)
	refreshExp := now.Add(s.refreshTTL)

	// Generate unique JTI for each token
	accessJTI := uuid.New().String()
	refreshJTI := uuid.New().String()

	// Create access token claims
	accessClaims := &Claims{
		UserID: user.ID,
		Email:  user.Email,
		Role:   user.Role,
		JTI:    accessJTI,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "system-stats",
			Audience:  jwt.ClaimStrings{"web-client"},
			Subject:   fmt.Sprintf("%d", user.ID),
			ExpiresAt: jwt.NewNumericDate(accessExp),
			NotBefore: jwt.NewNumericDate(now),
			IssuedAt:  jwt.NewNumericDate(now),
		},
	}

	// Create refresh token claims
	refreshClaims := &Claims{
		UserID: user.ID,
		Email:  user.Email,
		Role:   user.Role,
		JTI:    refreshJTI,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "system-stats",
			Audience:  jwt.ClaimStrings{"web-client"},
			Subject:   fmt.Sprintf("%d", user.ID),
			ExpiresAt: jwt.NewNumericDate(refreshExp),
			NotBefore: jwt.NewNumericDate(now),
			IssuedAt:  jwt.NewNumericDate(now),
		},
	}

	// Sign tokens
	accessToken := jwt.NewWithClaims(jwt.SigningMethodHS256, accessClaims)
	accessTokenString, err := accessToken.SignedString(accessSecret)
	if err != nil {
		return nil, fmt.Errorf("failed to sign access token: %w", err)
	}

	refreshToken := jwt.NewWithClaims(jwt.SigningMethodHS256, refreshClaims)
	refreshTokenString, err := refreshToken.SignedString(refreshSecret)
	if err != nil {
		return nil, fmt.Errorf("failed to sign refresh token: %w", err)
	}

	// Hash refresh token for storage
	hashedRefresh, err := s.HashRefreshToken(refreshTokenString)
	if err != nil {
		return nil, fmt.Errorf("failed to hash refresh token: %w", err)
	}

	// Persist refresh token
	err = s.PersistRefreshToken(ctx, user.ID, refreshJTI, hashedRefresh, refreshExp)
	if err != nil {
		return nil, fmt.Errorf("failed to persist refresh token: %w", err)
	}

	return &TokenPair{
		AccessToken:  accessTokenString,
		RefreshToken: refreshTokenString,
		ExpiresIn:    int64(s.accessTTL.Seconds()),
		AccessExp:    accessExp,
		RefreshExp:   refreshExp,
	}, nil
}

// ValidateAccessToken validates an access token and returns claims
func (s *tokenService) ValidateAccessToken(tokenString string) (*Claims, error) {
	accessSecret, _ := s.loadSecrets()
	if len(accessSecret) == 0 {
		return nil, fmt.Errorf("%w", ErrAuthSecretsNotConfigured)
	}
	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return accessSecret, nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to parse token: %w", err)
	}

	if claims, ok := token.Claims.(*Claims); ok && token.Valid {
		return claims, nil
	}

	return nil, fmt.Errorf("invalid token")
}

// ValidateRefreshToken validates a refresh token against the database
func (s *tokenService) ValidateRefreshToken(ctx context.Context, tokenString string) (*RefreshToken, error) {
	_, refreshSecret := s.loadSecrets()
	if len(refreshSecret) == 0 {
		return nil, fmt.Errorf("%w", ErrAuthSecretsNotConfigured)
	}
	// Parse token to get JTI
	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(token *jwt.Token) (interface{}, error) {
		return refreshSecret, nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to parse refresh token: %w", err)
	}

	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("invalid refresh token")
	}

	// Find token in database
	dbToken, err := s.refreshTokenRepo.FindByJTI(ctx, claims.JTI)
	if err != nil {
		return nil, fmt.Errorf("failed to find refresh token: %w", err)
	}
	if dbToken == nil {
		return nil, fmt.Errorf("refresh token not found")
	}

	// Verify token hash using SHA-256 digest comparison (constant time)
	sum := sha256.Sum256([]byte(tokenString))
	providedHash := hex.EncodeToString(sum[:])
	if subtle.ConstantTimeCompare([]byte(dbToken.TokenHash), []byte(providedHash)) != 1 {
		return nil, fmt.Errorf("invalid refresh token hash")
	}

	return dbToken, nil
}

// HashRefreshToken creates a SHA-256 hash of the refresh token
func (s *tokenService) HashRefreshToken(token string) (string, error) {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:]), nil
}

// PersistRefreshToken stores a refresh token in the database
func (s *tokenService) PersistRefreshToken(ctx context.Context, userID uint, jti, tokenHash string, expiresAt time.Time) error {
	token := &RefreshToken{
		UserID:    userID,
		JTI:       jti,
		TokenHash: tokenHash,
		ExpiresAt: expiresAt,
	}
	return s.refreshTokenRepo.Create(ctx, token)
}

// RevokeRefreshToken revokes a refresh token by JTI
func (s *tokenService) RevokeRefreshToken(ctx context.Context, jti string) error {
	return s.refreshTokenRepo.RevokeByJTI(ctx, jti)
}

// ConsumeRefreshToken atomically revokes a refresh token and reports whether this call won the race.
// Use this in /auth/refresh to ensure exactly one concurrent request can mint new tokens.
func (s *tokenService) AccessTokenTTL() time.Duration  { return s.accessTTL }
func (s *tokenService) RefreshTokenTTL() time.Duration { return s.refreshTTL }

func (s *tokenService) ConsumeRefreshToken(ctx context.Context, jti string) (bool, error) {
	return s.refreshTokenRepo.ConsumeByJTI(ctx, jti)
}

// RevokeAllUserTokens revokes all refresh tokens for a user
func (s *tokenService) RevokeAllUserTokens(ctx context.Context, userID uint) error {
	return s.refreshTokenRepo.RevokeAllByUserID(ctx, userID)
}
