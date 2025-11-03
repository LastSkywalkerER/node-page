package application

import (
    "context"
    "crypto/sha256"
    "crypto/subtle"
    "encoding/hex"
    "fmt"
    "time"

    "github.com/golang-jwt/jwt/v5"
    "github.com/google/uuid"

    localentities "system-stats/internal/modules/users/infrastructure/entities"
    userrepos "system-stats/internal/modules/users/infrastructure/repositories"
)

// TokenService handles JWT token operations
type TokenService interface {
	GenerateTokens(ctx context.Context, user *localentities.User) (*TokenPair, error)
	ValidateAccessToken(tokenString string) (*Claims, error)
	ValidateRefreshToken(ctx context.Context, tokenString string) (*localentities.RefreshToken, error)
	HashRefreshToken(token string) (string, error)
	PersistRefreshToken(ctx context.Context, userID uint, jti, tokenHash string, expiresAt time.Time) error
	RevokeRefreshToken(ctx context.Context, jti string) error
	RevokeAllUserTokens(ctx context.Context, userID uint) error
}

type tokenService struct {
	refreshTokenRepo userrepos.RefreshTokenRepository
	accessSecret     []byte
	refreshSecret    []byte
	accessTTL        time.Duration
	refreshTTL       time.Duration
}

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

// NewTokenService creates a new token service
func NewTokenService(
	refreshTokenRepo userrepos.RefreshTokenRepository,
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

// GenerateTokens generates a new pair of access and refresh tokens
func (s *tokenService) GenerateTokens(ctx context.Context, user *localentities.User) (*TokenPair, error) {
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
	accessTokenString, err := accessToken.SignedString(s.accessSecret)
	if err != nil {
		return nil, fmt.Errorf("failed to sign access token: %w", err)
	}

	refreshToken := jwt.NewWithClaims(jwt.SigningMethodHS256, refreshClaims)
	refreshTokenString, err := refreshToken.SignedString(s.refreshSecret)
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
	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return s.accessSecret, nil
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
func (s *tokenService) ValidateRefreshToken(ctx context.Context, tokenString string) (*localentities.RefreshToken, error) {
	// Parse token to get JTI
	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(token *jwt.Token) (interface{}, error) {
		return s.refreshSecret, nil
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

// HashRefreshToken creates a bcrypt hash of the refresh token
func (s *tokenService) HashRefreshToken(token string) (string, error) {
    sum := sha256.Sum256([]byte(token))
    return hex.EncodeToString(sum[:]), nil
}

// PersistRefreshToken stores a refresh token in the database
func (s *tokenService) PersistRefreshToken(ctx context.Context, userID uint, jti, tokenHash string, expiresAt time.Time) error {
	token := &localentities.RefreshToken{
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

// RevokeAllUserTokens revokes all refresh tokens for a user
func (s *tokenService) RevokeAllUserTokens(ctx context.Context, userID uint) error {
	return s.refreshTokenRepo.RevokeAllByUserID(ctx, userID)
}
