package presentation

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	userservice "system-stats/internal/modules/users/application"
)

// AuthHandler handles authentication-related HTTP requests
type AuthHandler struct {
	userService  userservice.UserService
	tokenService userservice.TokenService
	cookieSecure bool
}

// NewAuthHandler creates a new auth handler
func NewAuthHandler(userService userservice.UserService, tokenService userservice.TokenService, cookieSecure bool) *AuthHandler {
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
	c.SetCookie("access_token", accessToken, int(15*60), "/", "", h.cookieSecure, true)
	c.SetCookie("refresh_token", refreshToken, int(30*24*3600), "/api/v1/auth", "", h.cookieSecure, true)
}

func (h *AuthHandler) clearAuthCookies(c *gin.Context) {
	c.SetCookie("access_token", "", -1, "/", "", h.cookieSecure, true)
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
		c.JSON(http.StatusBadRequest, gin.H{
			"code":   "validation_error",
			"error":  "Invalid request data",
			"detail": err.Error(),
		})
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
		status := http.StatusInternalServerError
		code := "internal_error"
		errorMsg := "Failed to register user"

		if strings.Contains(err.Error(), "registration is disabled") {
			status = http.StatusForbidden
			code = "registration_disabled"
			errorMsg = "Registration is disabled. Users already exist in the system."
		} else if strings.Contains(err.Error(), "invalid invitation") {
			status = http.StatusBadRequest
			code = "invalid_invitation"
			errorMsg = "Invalid or already used invitation link."
		} else if strings.Contains(err.Error(), "invitation email mismatch") {
			status = http.StatusBadRequest
			code = "invitation_email_mismatch"
			// Extract expected email from "invitation is for user@example.com"
			if idx := strings.Index(err.Error(), "invitation is for "); idx >= 0 {
				expectedEmail := strings.TrimSpace(err.Error()[idx+len("invitation is for "):])
				errorMsg = "This invitation is for " + expectedEmail + ". Please use that email."
				c.JSON(status, gin.H{"code": code, "error": errorMsg, "expected_email": expectedEmail})
				return
			}
			errorMsg = "Email must match the invited address."
		} else if strings.Contains(err.Error(), "already exists") {
			status = http.StatusConflict
			code = "email_already_exists"
			errorMsg = "User with this email already exists"
		} else if strings.Contains(err.Error(), "password") {
			status = http.StatusBadRequest
			code = "validation_error"
			errorMsg = err.Error()
		}

		c.JSON(status, gin.H{"code": code, "error": errorMsg})
		return
	}

	tokenPair, err := h.tokenService.GenerateTokens(c.Request.Context(), user)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code":  "token_generation_error",
			"error": "Failed to generate tokens",
		})
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
		c.JSON(http.StatusBadRequest, gin.H{
			"code":   "validation_error",
			"error":  "Invalid request data",
			"detail": err.Error(),
		})
		return
	}

	user, err := h.userService.Login(c.Request.Context(), req.Email, req.Password)
	if err != nil {
		status := http.StatusInternalServerError
		code := "internal_error"
		errorMsg := "Failed to authenticate user"

		if strings.Contains(err.Error(), "invalid credentials") {
			status = http.StatusUnauthorized
			code = "invalid_credentials"
			errorMsg = "Invalid email or password"
		}

		c.JSON(status, gin.H{"code": code, "error": errorMsg})
		return
	}

	tokenPair, err := h.tokenService.GenerateTokens(c.Request.Context(), user)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code":  "token_generation_error",
			"error": "Failed to generate tokens",
		})
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
			c.JSON(http.StatusBadRequest, gin.H{
				"code":  "validation_error",
				"error": "Refresh token required",
			})
			return
		}
		refreshToken = req.RefreshToken
	}

	dbToken, err := h.tokenService.ValidateRefreshToken(c.Request.Context(), refreshToken)
	if err != nil {
		h.clearAuthCookies(c)
		c.JSON(http.StatusUnauthorized, gin.H{
			"code":  "invalid_or_revoked_refresh",
			"error": "Invalid or revoked refresh token",
		})
		return
	}

	tokenPair, err := h.tokenService.GenerateTokens(c.Request.Context(), &dbToken.User)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code":  "token_generation_error",
			"error": "Failed to generate tokens",
		})
		return
	}

	// Revoke old token after successful generation
	if err := h.tokenService.RevokeRefreshToken(c.Request.Context(), dbToken.JTI); err != nil {
		c.Error(err)
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
		c.JSON(http.StatusUnauthorized, gin.H{
			"code":  "unauthorized",
			"error": "Authentication required",
		})
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

	c.JSON(http.StatusNoContent, nil)
}
