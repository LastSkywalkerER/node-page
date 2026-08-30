package users

import (
	"errors"
	"net/http"
	"time"

	"github.com/charmbracelet/log"
	"github.com/gin-gonic/gin"

	"system-stats/internal/app/apperror"
)

// AuthHandler handles authentication-related HTTP requests
type AuthHandler struct {
	userService  UserService
	tokenService TokenService
	cookieSecure bool
}

// NewAuthHandler creates a new auth handler
func NewAuthHandler(userService UserService, tokenService TokenService, cookieSecure bool) *AuthHandler {
	if userService == nil {
		panic("userService cannot be nil")
	}
	if tokenService == nil {
		panic("tokenService cannot be nil")
	}
	return &AuthHandler{
		userService:  userService,
		tokenService: tokenService,
		cookieSecure: cookieSecure,
	}
}

// RegisterRequest represents a user registration request
type RegisterRequest struct {
	Email       string `json:"email" binding:"required,email"`
	Password    string `json:"password" binding:"required,min=8"`
	InviteToken string `json:"invite_token"`
}

// LoginRequest represents a user login request
type LoginRequest struct {
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required,min=8"`
}

// RefreshRequest represents an optional token refresh request body (cookie is preferred)
type RefreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

// LogoutRequest represents a logout request
type LogoutRequest struct {
	RefreshToken string `json:"refresh_token,omitempty"`
}

// UserResponse represents user data in responses
type UserResponse struct {
	ID    uint   `json:"id"`
	Email string `json:"email"`
	Role  string `json:"role"`
}

func (h *AuthHandler) setAuthCookies(c *gin.Context, accessToken, refreshToken string, expiresIn int64) {
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie("access_token", accessToken, int(h.tokenService.AccessTokenTTL().Seconds()), "/", "", h.cookieSecure, true)
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie("refresh_token", refreshToken, int(h.tokenService.RefreshTokenTTL().Seconds()), "/api/v1/auth", "", h.cookieSecure, true)
}

// refreshGraceWindow tolerates the multi-tab rotation race: cookies are
// shared browser-wide, every open tab arms its own proactive refresh timer,
// and they fire within the same second — the losers arrive with the token
// the winner just rotated. Within this window that's a benign race (the
// strict single-use 401 used to clear cookies and log out the WHOLE
// browser); outside it, single-use stays enforced against real replays.
const refreshGraceWindow = 60 * time.Second

func (h *AuthHandler) clearAuthCookies(c *gin.Context) {
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie("access_token", "", -1, "/", "", h.cookieSecure, true)
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie("refresh_token", "", -1, "/api/v1/auth", "", h.cookieSecure, true)
}

// Register handles user registration
//
// @Summary     Register user
// @Description Creates a new user account. Disabled when users already exist (first-time setup only).
// @Tags        auth
// @Accept      json
// @Produce     json
// @Param       body  body      RegisterRequest  true  "Registration credentials"
// @Success     200   {object}  map[string]interface{}
// @Failure     400   {object}  map[string]string
// @Failure     403   {object}  map[string]string
// @Failure     409   {object}  map[string]string
// @Failure     500   {object}  map[string]string
// @Router      /auth/register [post]
func (h *AuthHandler) Register(c *gin.Context) {
	var req RegisterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(apperror.WithDetail(apperror.BadRequest("validation_error", "Invalid request data"), err.Error()))
		return
	}

	// Allow invite_token from query param as fallback (e.g. when form doesn't include it)
	if req.InviteToken == "" {
		req.InviteToken = c.Query("invite")
	}

	var invitePtr *string
	if req.InviteToken != "" {
		invitePtr = &req.InviteToken
	}

	user, err := h.userService.Register(c.Request.Context(), req.Email, req.Password, invitePtr)
	if err != nil {
		switch {
		case errors.Is(err, ErrRegistrationDisabled):
			log.Warn("Registration attempt while disabled", "email", req.Email, "ip", c.ClientIP())
			_ = c.Error(apperror.Forbidden("registration_disabled", "Registration is disabled. Users already exist in the system."))
		case errors.Is(err, ErrInvitationEmailMismatch):
			_ = c.Error(apperror.BadRequest("invitation_email_mismatch", "Email must match the invited address."))
		case errors.Is(err, ErrInvalidInvitation):
			log.Warn("Invalid invitation token used", "email", req.Email, "ip", c.ClientIP())
			_ = c.Error(apperror.BadRequest("invalid_invitation", "Invalid or already used invitation link."))
		case errors.Is(err, ErrEmailExists):
			log.Warn("Registration with existing email", "email", req.Email, "ip", c.ClientIP())
			_ = c.Error(apperror.Conflict("email_already_exists", "User with this email already exists"))
		default:
			log.Error("Registration error", "email", req.Email, "ip", c.ClientIP(), "error", err)
			_ = c.Error(apperror.Internal("internal_error", "Failed to register user"))
		}
		return
	}

	tokenPair, err := h.tokenService.GenerateTokens(c.Request.Context(), user)
	if err != nil {
		if errors.Is(err, ErrAuthSecretsNotConfigured) {
			_ = c.Error(apperror.Wrap(err, "auth_not_configured", "Complete initial setup or set JWT_SECRET and REFRESH_SECRET, then restart the server.", http.StatusServiceUnavailable))
		} else {
			_ = c.Error(apperror.Internal("token_generation_error", "Failed to generate tokens"))
		}
		return
	}

	h.setAuthCookies(c, tokenPair.AccessToken, tokenPair.RefreshToken, tokenPair.ExpiresIn)

	c.JSON(http.StatusOK, gin.H{"data": gin.H{
		"user":       UserResponse{ID: user.ID, Email: user.Email, Role: user.Role},
		"expires_in": tokenPair.ExpiresIn,
	}})
}

// Login handles user authentication
//
// @Summary     Login
// @Description Authenticates a user and sets HttpOnly auth cookies. Also returns tokens in the response body.
// @Tags        auth
// @Accept      json
// @Produce     json
// @Param       body  body      LoginRequest  true  "Login credentials"
// @Success     200   {object}  map[string]interface{}
// @Failure     400   {object}  map[string]string
// @Failure     401   {object}  map[string]string
// @Failure     500   {object}  map[string]string
// @Router      /auth/login [post]
func (h *AuthHandler) Login(c *gin.Context) {
	var req LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(apperror.WithDetail(apperror.BadRequest("validation_error", "Invalid request data"), err.Error()))
		return
	}

	user, err := h.userService.Login(c.Request.Context(), req.Email, req.Password)
	if err != nil {
		if errors.Is(err, ErrInvalidCredentials) {
			log.Warn("Login failed", "email", req.Email, "ip", c.ClientIP(), "ua", c.GetHeader("User-Agent"))
			_ = c.Error(apperror.Unauthorized("invalid_credentials", "Invalid email or password"))
		} else {
			log.Error("Login error", "email", req.Email, "ip", c.ClientIP(), "error", err)
			_ = c.Error(apperror.Internal("internal_error", "Failed to authenticate user"))
		}
		return
	}

	tokenPair, err := h.tokenService.GenerateTokens(c.Request.Context(), user)
	if err != nil {
		if errors.Is(err, ErrAuthSecretsNotConfigured) {
			_ = c.Error(apperror.Wrap(err, "auth_not_configured", "Complete initial setup or set JWT_SECRET and REFRESH_SECRET, then restart the server.", http.StatusServiceUnavailable))
		} else {
			_ = c.Error(apperror.Internal("token_generation_error", "Failed to generate tokens"))
		}
		return
	}

	h.setAuthCookies(c, tokenPair.AccessToken, tokenPair.RefreshToken, tokenPair.ExpiresIn)

	c.JSON(http.StatusOK, gin.H{"data": gin.H{
		"user":       UserResponse{ID: user.ID, Email: user.Email, Role: user.Role},
		"expires_in": tokenPair.ExpiresIn,
	}})
}

// Refresh handles token refresh
//
// @Summary     Refresh token
// @Description Issues a new access token using the refresh token from cookie or request body.
// @Tags        auth
// @Accept      json
// @Produce     json
// @Param       body  body      RefreshRequest  false  "Refresh token (optional if cookie is set)"
// @Success     200   {object}  map[string]interface{}
// @Failure     400   {object}  map[string]string
// @Failure     401   {object}  map[string]string
// @Failure     500   {object}  map[string]string
// @Router      /auth/refresh [post]
func (h *AuthHandler) Refresh(c *gin.Context) {
	// Prefer refresh token from cookie; fall back to request body
	refreshToken, err := c.Cookie("refresh_token")
	if err != nil || refreshToken == "" {
		var req RefreshRequest
		if err := c.ShouldBindJSON(&req); err != nil || req.RefreshToken == "" {
			_ = c.Error(apperror.BadRequest("validation_error", "Refresh token required"))
			return
		}
		refreshToken = req.RefreshToken
	}

	dbToken, err := h.tokenService.ValidateRefreshToken(c.Request.Context(), refreshToken)
	if err != nil {
		h.clearAuthCookies(c)
		log.Warn("Refresh token rejected", "ip", c.ClientIP(), "ua", c.GetHeader("User-Agent"), "error", err)
		if errors.Is(err, ErrAuthSecretsNotConfigured) {
			_ = c.Error(apperror.Wrap(err, "auth_not_configured", "Complete initial setup or set JWT_SECRET and REFRESH_SECRET, then restart the server.", http.StatusServiceUnavailable))
		} else {
			_ = c.Error(apperror.Unauthorized("invalid_or_revoked_refresh", "Invalid or revoked refresh token"))
		}
		return
	}

	// Atomically consume the refresh token. Only the winner of any concurrent
	// refresh race proceeds; replays and parallel losers get 401.
	consumed, err := h.tokenService.ConsumeRefreshToken(c.Request.Context(), dbToken.JTI)
	if err != nil {
		_ = c.Error(apperror.Internal("internal_error", "Failed to consume refresh token"))
		return
	}
	if !consumed {
		// Re-read: our pre-consume copy has a stale RevokedAt.
		if fresh, ferr := h.tokenService.ValidateRefreshToken(c.Request.Context(), refreshToken); ferr == nil &&
			fresh.RevokedAt != nil && time.Since(*fresh.RevokedAt) <= refreshGraceWindow {
			tokenPair, gerr := h.tokenService.GenerateTokens(c.Request.Context(), &fresh.User)
			if gerr == nil {
				h.setAuthCookies(c, tokenPair.AccessToken, tokenPair.RefreshToken, tokenPair.ExpiresIn)
				c.JSON(http.StatusOK, gin.H{"data": gin.H{"expires_in": tokenPair.ExpiresIn}})
				return
			}
		}
		h.clearAuthCookies(c)
		log.Warn("Refresh token replay or concurrent reuse", "user_id", dbToken.UserID, "jti", dbToken.JTI, "ip", c.ClientIP())
		_ = c.Error(apperror.Unauthorized("token_already_used", "Refresh token already used"))
		return
	}

	tokenPair, err := h.tokenService.GenerateTokens(c.Request.Context(), &dbToken.User)
	if err != nil {
		if errors.Is(err, ErrAuthSecretsNotConfigured) {
			_ = c.Error(apperror.Wrap(err, "auth_not_configured", "Complete initial setup or set JWT_SECRET and REFRESH_SECRET, then restart the server.", http.StatusServiceUnavailable))
		} else {
			_ = c.Error(apperror.Internal("token_generation_error", "Failed to generate tokens"))
		}
		return
	}

	h.setAuthCookies(c, tokenPair.AccessToken, tokenPair.RefreshToken, tokenPair.ExpiresIn)

	c.JSON(http.StatusOK, gin.H{"data": gin.H{
		"expires_in": tokenPair.ExpiresIn,
	}})
}

// Logout handles user logout
//
// @Summary     Logout
// @Description Revokes the refresh token and clears auth cookies.
// @Tags        auth
// @Accept      json
// @Produce     json
// @Param       body  body      LogoutRequest  false  "Refresh token to revoke (optional)"
// @Success     204   "No Content"
// @Failure     401   {object}  map[string]string
// @Security    BearerAuth
// @Router      /auth/logout [post]
func (h *AuthHandler) Logout(c *gin.Context) {
	var req LogoutRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		req = LogoutRequest{}
	}

	// Also check cookie for refresh token to revoke
	if req.RefreshToken == "" {
		if cookie, err := c.Cookie("refresh_token"); err == nil {
			req.RefreshToken = cookie
		}
	}

	userID, exists := c.Get("userID")
	if !exists {
		_ = c.Error(apperror.Unauthorized("unauthorized", "Authentication required"))
		return
	}

	if req.RefreshToken != "" {
		dbToken, err := h.tokenService.ValidateRefreshToken(c.Request.Context(), req.RefreshToken)
		if err == nil && dbToken != nil && dbToken.UserID == userID.(uint) {
			h.tokenService.RevokeRefreshToken(c.Request.Context(), dbToken.JTI)
		}
	} else {
		h.tokenService.RevokeAllUserTokens(c.Request.Context(), userID.(uint))
	}

	h.clearAuthCookies(c)

	c.Status(http.StatusNoContent)
}
