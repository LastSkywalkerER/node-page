package middleware_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"

	"system-stats/internal/app/middleware"
	userservice "system-stats/internal/modules/users/application"
	userentities "system-stats/internal/modules/users/infrastructure/entities"
)

const testAccessSecret = "test-access-secret"

type mockTokenServiceForAuth struct {
	secret []byte
}

func (m *mockTokenServiceForAuth) GenerateTokens(_ context.Context, _ *userentities.User) (*userservice.TokenPair, error) {
	return nil, nil
}

func (m *mockTokenServiceForAuth) ValidateAccessToken(tokenString string) (*userservice.Claims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &userservice.Claims{}, func(token *jwt.Token) (any, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return m.secret, nil
	})
	if err != nil {
		return nil, err
	}
	if claims, ok := token.Claims.(*userservice.Claims); ok && token.Valid {
		return claims, nil
	}
	return nil, fmt.Errorf("invalid token")
}

func (m *mockTokenServiceForAuth) ValidateRefreshToken(_ context.Context, _ string) (*userentities.RefreshToken, error) {
	return nil, nil
}
func (m *mockTokenServiceForAuth) HashRefreshToken(_ string) (string, error) { return "", nil }
func (m *mockTokenServiceForAuth) PersistRefreshToken(_ context.Context, _ uint, _, _ string, _ time.Time) error {
	return nil
}
func (m *mockTokenServiceForAuth) RevokeRefreshToken(_ context.Context, _ string) error  { return nil }
func (m *mockTokenServiceForAuth) RevokeAllUserTokens(_ context.Context, _ uint) error { return nil }

var _ userservice.TokenService = (*mockTokenServiceForAuth)(nil)

func signTestToken(secret string, userID uint, email, role string, expiry time.Time) string {
	claims := &userservice.Claims{
		UserID: userID,
		Email:  email,
		Role:   role,
		JTI:    "test-jti",
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(expiry),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	s, _ := token.SignedString([]byte(secret))
	return s
}

func TestAuthJWT_FromCookie(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ts := &mockTokenServiceForAuth{secret: []byte(testAccessSecret)}
	tok := signTestToken(testAccessSecret, 1, "a@b.com", "ADMIN", time.Now().Add(time.Hour))

	var gotUID any
	var gotRole any
	r := gin.New()
	r.Use(middleware.AuthJWT(ts))
	r.GET("/test", func(c *gin.Context) {
		gotUID, _ = c.Get("userID")
		gotRole, _ = c.Get("userRole")
		c.Status(200)
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.AddCookie(&http.Cookie{Name: "access_token", Value: tok})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	if gotUID.(uint) != 1 {
		t.Errorf("userID = %v, want 1", gotUID)
	}
	if gotRole.(string) != "ADMIN" {
		t.Errorf("userRole = %v, want ADMIN", gotRole)
	}
}

func TestAuthJWT_FromHeader(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ts := &mockTokenServiceForAuth{secret: []byte(testAccessSecret)}
	tok := signTestToken(testAccessSecret, 2, "b@c.com", "USER", time.Now().Add(time.Hour))

	var gotUID any
	r := gin.New()
	r.Use(middleware.AuthJWT(ts))
	r.GET("/test", func(c *gin.Context) {
		gotUID, _ = c.Get("userID")
		c.Status(200)
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if gotUID.(uint) != 2 {
		t.Errorf("userID = %v, want 2", gotUID)
	}
}

func TestAuthJWT_MissingToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ts := &mockTokenServiceForAuth{secret: []byte(testAccessSecret)}

	r := gin.New()
	r.Use(middleware.AuthJWT(ts))
	r.GET("/test", func(c *gin.Context) { c.Status(200) })

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestAuthJWT_InvalidToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ts := &mockTokenServiceForAuth{secret: []byte(testAccessSecret)}

	r := gin.New()
	r.Use(middleware.AuthJWT(ts))
	r.GET("/test", func(c *gin.Context) { c.Status(200) })

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.AddCookie(&http.Cookie{Name: "access_token", Value: "garbage.token.value"})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestRequireAdmin_AdminRole(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("userRole", "ADMIN")
		c.Next()
	})
	r.Use(middleware.RequireAdmin())
	r.GET("/test", func(c *gin.Context) { c.Status(200) })

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestRequireAdmin_UserRole(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("userRole", "USER")
		c.Next()
	})
	r.Use(middleware.RequireAdmin())
	r.GET("/test", func(c *gin.Context) { c.Status(200) })

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}
