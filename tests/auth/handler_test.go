package auth_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"system-stats/internal/app/middleware"
	users "system-stats/internal/auth/users"
)

type mockUserService struct {
	registerUser *users.User
	registerErr  error
	loginUser    *users.User
	loginErr     error
}

func (m *mockUserService) Register(_ context.Context, _, _ string, _ *string) (*users.User, error) {
	return m.registerUser, m.registerErr
}
func (m *mockUserService) Login(_ context.Context, _, _ string) (*users.User, error) {
	return m.loginUser, m.loginErr
}
func (m *mockUserService) GetByID(_ context.Context, _ uint) (*users.User, error) {
	return nil, nil
}
func (m *mockUserService) GetByEmail(_ context.Context, _ string) (*users.User, error) {
	return nil, nil
}
func (m *mockUserService) List(_ context.Context, _, _ int) ([]*users.User, error) {
	return nil, nil
}
func (m *mockUserService) UpdateRole(_ context.Context, _, _ uint, _ string) error { return nil }
func (m *mockUserService) Delete(_ context.Context, _, _ uint) error               { return nil }
func (m *mockUserService) Count(_ context.Context) (int64, error)                  { return 0, nil }
func (m *mockUserService) HashPassword(_ string) (string, error)                { return "", nil }
func (m *mockUserService) VerifyPassword(_, _ string) error                     { return nil }

var _ users.UserService = (*mockUserService)(nil)

type mockTokenService struct {
	tokenPair       *users.TokenPair
	generateErr     error
	accessClaims    *users.Claims
	accessErr       error
	refreshToken    *users.RefreshToken
	refreshErr      error
	revokeErr       error
	revokeAllErr    error
	hashResult      string
	hashErr         error
	persistErr      error
	consumeOk       bool
	consumeErr      error
	revokeCalled    bool
	revokeAllCalled bool
	consumeCalled   bool
}

func (m *mockTokenService) GenerateTokens(_ context.Context, _ *users.User) (*users.TokenPair, error) {
	return m.tokenPair, m.generateErr
}
func (m *mockTokenService) ValidateAccessToken(_ string) (*users.Claims, error) {
	return m.accessClaims, m.accessErr
}
func (m *mockTokenService) ValidateRefreshToken(_ context.Context, _ string) (*users.RefreshToken, error) {
	return m.refreshToken, m.refreshErr
}
func (m *mockTokenService) HashRefreshToken(_ string) (string, error) {
	return m.hashResult, m.hashErr
}
func (m *mockTokenService) PersistRefreshToken(_ context.Context, _ uint, _, _ string, _ time.Time) error {
	return m.persistErr
}
func (m *mockTokenService) RevokeRefreshToken(_ context.Context, _ string) error {
	m.revokeCalled = true
	return m.revokeErr
}
func (m *mockTokenService) ConsumeRefreshToken(_ context.Context, _ string) (bool, error) {
	m.consumeCalled = true
	return m.consumeOk, m.consumeErr
}
func (m *mockTokenService) RevokeAllUserTokens(_ context.Context, _ uint) error {
	m.revokeAllCalled = true
	return m.revokeAllErr
}

var _ users.TokenService = (*mockTokenService)(nil)

func setupRouter(h *users.AuthHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.ErrorHandler())
	return r
}

func jsonBody(t *testing.T, v any) *bytes.Buffer {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return bytes.NewBuffer(b)
}

func parseJSON(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("failed to parse response: %v\nbody: %s", err, rec.Body.String())
	}
	return out
}

var handlerTokenPair = &users.TokenPair{
	AccessToken:  "access-tok",
	RefreshToken: "refresh-tok",
	ExpiresIn:    900,
	AccessExp:    time.Now().Add(15 * time.Minute),
	RefreshExp:   time.Now().Add(7 * 24 * time.Hour),
}

var handlerDefaultUser = &users.User{
	ID:    1,
	Email: "test@example.com",
	Role:  "ADMIN",
}

func TestHandler_Register_Success(t *testing.T) {
	us := &mockUserService{registerUser: handlerDefaultUser}
	ts := &mockTokenService{tokenPair: handlerTokenPair}
	h := users.NewAuthHandler(us, ts, false)
	r := setupRouter(h)
	r.POST("/auth/register", h.Register)

	body := jsonBody(t, map[string]string{"email": "test@example.com", "password": "password123"})
	req := httptest.NewRequest(http.MethodPost, "/auth/register", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}

	cookies := rec.Result().Cookies()
	found := false
	for _, c := range cookies {
		if c.Name == "access_token" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected access_token cookie to be set")
	}

	resp := parseJSON(t, rec)
	data, ok := resp["data"].(map[string]any)
	if !ok {
		t.Fatal("missing data field")
	}
	if data["expires_in"] == nil {
		t.Error("missing expires_in")
	}
}

func TestHandler_Register_ValidationError(t *testing.T) {
	h := users.NewAuthHandler(&mockUserService{}, &mockTokenService{}, false)
	r := setupRouter(h)
	r.POST("/auth/register", h.Register)

	body := jsonBody(t, map[string]string{"email": "bad", "password": "short"})
	req := httptest.NewRequest(http.MethodPost, "/auth/register", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", rec.Code, rec.Body.String())
	}
}

func TestHandler_Register_RegistrationDisabled(t *testing.T) {
	us := &mockUserService{registerErr: users.ErrRegistrationDisabled}
	h := users.NewAuthHandler(us, &mockTokenService{}, false)
	r := setupRouter(h)
	r.POST("/auth/register", h.Register)

	body := jsonBody(t, map[string]string{"email": "test@example.com", "password": "password123"})
	req := httptest.NewRequest(http.MethodPost, "/auth/register", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body: %s", rec.Code, rec.Body.String())
	}
}

func TestHandler_Register_EmailExists(t *testing.T) {
	us := &mockUserService{registerErr: users.ErrEmailExists}
	h := users.NewAuthHandler(us, &mockTokenService{}, false)
	r := setupRouter(h)
	r.POST("/auth/register", h.Register)

	body := jsonBody(t, map[string]string{"email": "test@example.com", "password": "password123"})
	req := httptest.NewRequest(http.MethodPost, "/auth/register", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body: %s", rec.Code, rec.Body.String())
	}
}

func TestHandler_Login_Success(t *testing.T) {
	us := &mockUserService{loginUser: handlerDefaultUser}
	ts := &mockTokenService{tokenPair: handlerTokenPair}
	h := users.NewAuthHandler(us, ts, false)
	r := setupRouter(h)
	r.POST("/auth/login", h.Login)

	body := jsonBody(t, map[string]string{"email": "test@example.com", "password": "password123"})
	req := httptest.NewRequest(http.MethodPost, "/auth/login", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}

	cookies := rec.Result().Cookies()
	found := false
	for _, c := range cookies {
		if c.Name == "access_token" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected access_token cookie to be set")
	}
}

func TestHandler_Login_InvalidCredentials(t *testing.T) {
	us := &mockUserService{loginErr: users.ErrInvalidCredentials}
	h := users.NewAuthHandler(us, &mockTokenService{}, false)
	r := setupRouter(h)
	r.POST("/auth/login", h.Login)

	body := jsonBody(t, map[string]string{"email": "test@example.com", "password": "wrongpassword"})
	req := httptest.NewRequest(http.MethodPost, "/auth/login", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body: %s", rec.Code, rec.Body.String())
	}
}

func TestHandler_Refresh_MissingToken(t *testing.T) {
	h := users.NewAuthHandler(&mockUserService{}, &mockTokenService{}, false)
	r := setupRouter(h)
	r.POST("/auth/refresh", h.Refresh)

	req := httptest.NewRequest(http.MethodPost, "/auth/refresh", bytes.NewBuffer([]byte("{}")))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", rec.Code, rec.Body.String())
	}
}

func TestHandler_Logout_ClearsCookies(t *testing.T) {
	ts := &mockTokenService{}
	h := users.NewAuthHandler(&mockUserService{}, ts, false)
	r := setupRouter(h)
	r.POST("/auth/logout", func(c *gin.Context) {
		c.Set("userID", uint(1))
		c.Next()
	}, h.Logout)

	req := httptest.NewRequest(http.MethodPost, "/auth/logout", bytes.NewBuffer([]byte("{}")))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body: %s", rec.Code, rec.Body.String())
	}

	cleared := false
	for _, c := range rec.Result().Cookies() {
		if c.Name == "access_token" && c.MaxAge < 0 {
			cleared = true
			break
		}
	}
	if !cleared {
		t.Error("expected access_token cookie to be cleared")
	}
	if !ts.revokeAllCalled {
		t.Error("expected RevokeAllUserTokens to be called")
	}
}
