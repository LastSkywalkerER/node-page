package auth_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	userservice "system-stats/internal/modules/users/application"
	userentities "system-stats/internal/modules/users/infrastructure/entities"
	userrepos "system-stats/internal/modules/users/infrastructure/repositories"
)

type mockRefreshTokenRepository struct {
	created   *userentities.RefreshToken
	createErr error

	foundToken *userentities.RefreshToken
	findErr    error

	revokeJTI    string
	revokeErr    error
	revokeAllUID uint
	revokeAllErr error
}

var _ userrepos.RefreshTokenRepository = (*mockRefreshTokenRepository)(nil)

func (m *mockRefreshTokenRepository) Create(_ context.Context, token *userentities.RefreshToken) error {
	m.created = token
	return m.createErr
}

func (m *mockRefreshTokenRepository) FindByJTI(_ context.Context, jti string) (*userentities.RefreshToken, error) {
	if m.findErr != nil {
		return nil, m.findErr
	}
	if m.foundToken != nil && m.foundToken.JTI == jti {
		return m.foundToken, nil
	}
	return nil, nil
}

func (m *mockRefreshTokenRepository) RevokeByJTI(_ context.Context, jti string) error {
	m.revokeJTI = jti
	return m.revokeErr
}

func (m *mockRefreshTokenRepository) RevokeAllByUserID(_ context.Context, userID uint) error {
	m.revokeAllUID = userID
	return m.revokeAllErr
}

func (m *mockRefreshTokenRepository) DeleteExpired(_ context.Context) error {
	return nil
}

func newTestTokenService(repo userrepos.RefreshTokenRepository) userservice.TokenService {
	return userservice.NewTokenService(repo, "test-access-secret", "test-refresh-secret", 15*time.Minute, 7*24*time.Hour)
}

var tokenTestUser = &userentities.User{
	ID:    1,
	Email: "test@example.com",
	Role:  "ADMIN",
}

func TestTokenService_GenerateTokens_Success(t *testing.T) {
	mock := &mockRefreshTokenRepository{}
	svc := newTestTokenService(mock)

	pair, err := svc.GenerateTokens(context.Background(), tokenTestUser)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pair.AccessToken == "" {
		t.Error("access token is empty")
	}
	if pair.RefreshToken == "" {
		t.Error("refresh token is empty")
	}
	if pair.ExpiresIn != int64((15 * time.Minute).Seconds()) {
		t.Errorf("ExpiresIn = %d, want %d", pair.ExpiresIn, int64((15*time.Minute).Seconds()))
	}
	if mock.created == nil {
		t.Fatal("expected refresh token to be persisted")
	}
	if mock.created.UserID != tokenTestUser.ID {
		t.Errorf("persisted UserID = %d, want %d", mock.created.UserID, tokenTestUser.ID)
	}
	if mock.created.TokenHash == "" {
		t.Error("persisted TokenHash is empty")
	}
}

func TestTokenService_ValidateAccessToken_Valid(t *testing.T) {
	mock := &mockRefreshTokenRepository{}
	svc := newTestTokenService(mock)

	pair, err := svc.GenerateTokens(context.Background(), tokenTestUser)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	claims, err := svc.ValidateAccessToken(pair.AccessToken)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if claims.UserID != tokenTestUser.ID {
		t.Errorf("UserID = %d, want %d", claims.UserID, tokenTestUser.ID)
	}
	if claims.Email != tokenTestUser.Email {
		t.Errorf("Email = %q, want %q", claims.Email, tokenTestUser.Email)
	}
	if claims.Role != tokenTestUser.Role {
		t.Errorf("Role = %q, want %q", claims.Role, tokenTestUser.Role)
	}
}

func TestTokenService_ValidateAccessToken_Expired(t *testing.T) {
	svc := newTestTokenService(&mockRefreshTokenRepository{})

	now := time.Now().Add(-time.Hour)
	claims := &userservice.Claims{
		UserID: 1,
		Email:  "test@example.com",
		Role:   "ADMIN",
		JTI:    "expired-jti",
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(now),
			IssuedAt:  jwt.NewNumericDate(now.Add(-time.Hour)),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenStr, _ := token.SignedString([]byte("test-access-secret"))

	_, err := svc.ValidateAccessToken(tokenStr)
	if err == nil {
		t.Fatal("expected error for expired token")
	}
}

func TestTokenService_ValidateAccessToken_WrongSecret(t *testing.T) {
	svc := newTestTokenService(&mockRefreshTokenRepository{})

	claims := &userservice.Claims{
		UserID: 1,
		Email:  "test@example.com",
		Role:   "ADMIN",
		JTI:    "wrong-secret-jti",
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenStr, _ := token.SignedString([]byte("wrong-secret"))

	_, err := svc.ValidateAccessToken(tokenStr)
	if err == nil {
		t.Fatal("expected error for token signed with wrong secret")
	}
}

func TestTokenService_ValidateRefreshToken_Valid(t *testing.T) {
	mock := &mockRefreshTokenRepository{}
	svc := newTestTokenService(mock)

	pair, err := svc.GenerateTokens(context.Background(), tokenTestUser)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	mock.foundToken = mock.created
	mock.foundToken.User = *tokenTestUser

	dbToken, err := svc.ValidateRefreshToken(context.Background(), pair.RefreshToken)
	if err != nil {
		t.Fatalf("validate refresh: %v", err)
	}
	if dbToken.UserID != tokenTestUser.ID {
		t.Errorf("UserID = %d, want %d", dbToken.UserID, tokenTestUser.ID)
	}
}

func TestTokenService_ValidateRefreshToken_Revoked(t *testing.T) {
	mock := &mockRefreshTokenRepository{}
	svc := newTestTokenService(mock)

	pair, err := svc.GenerateTokens(context.Background(), tokenTestUser)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	_, err = svc.ValidateRefreshToken(context.Background(), pair.RefreshToken)
	if err == nil {
		t.Fatal("expected error for revoked/missing refresh token")
	}
}

func TestTokenService_RevokeRefreshToken(t *testing.T) {
	mock := &mockRefreshTokenRepository{}
	svc := newTestTokenService(mock)

	jti := "some-jti"
	if err := svc.RevokeRefreshToken(context.Background(), jti); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mock.revokeJTI != jti {
		t.Errorf("revoked JTI = %q, want %q", mock.revokeJTI, jti)
	}
}

func TestTokenService_RevokeRefreshToken_RepoError(t *testing.T) {
	mock := &mockRefreshTokenRepository{revokeErr: fmt.Errorf("db down")}
	svc := newTestTokenService(mock)

	err := svc.RevokeRefreshToken(context.Background(), "jti")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestTokenService_RevokeAllUserTokens(t *testing.T) {
	mock := &mockRefreshTokenRepository{}
	svc := newTestTokenService(mock)

	if err := svc.RevokeAllUserTokens(context.Background(), 42); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mock.revokeAllUID != 42 {
		t.Errorf("revoked UID = %d, want 42", mock.revokeAllUID)
	}
}

func TestTokenService_RevokeAllUserTokens_RepoError(t *testing.T) {
	mock := &mockRefreshTokenRepository{revokeAllErr: fmt.Errorf("db down")}
	svc := newTestTokenService(mock)

	err := svc.RevokeAllUserTokens(context.Background(), 1)
	if err == nil {
		t.Fatal("expected error")
	}
}
