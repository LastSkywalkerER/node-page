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
}

// NewAuthHandler creates a new auth handler
func NewAuthHandler(userService userservice.UserService, tokenService userservice.TokenService) *AuthHandler {
	if userService == nil {
		panic("userService cannot be nil")
	}
	if tokenService == nil {
		panic("tokenService cannot be nil")
	}
	return &AuthHandler{
		userService:  userService,
		tokenService: tokenService,
	}
}

// RegisterRequest represents a user registration request
type RegisterRequest struct {
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required,min=8"`
}

// RegisterResponse represents a registration response
type RegisterResponse struct {
	User         *UserResponse `json:"user"`
	AccessToken  string        `json:"access_token"`
	RefreshToken string        `json:"refresh_token"`
	ExpiresIn    int64         `json:"expires_in"`
}

// LoginRequest represents a user login request
type LoginRequest struct {
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required,min=8"`
}

// LoginResponse represents a login response
type LoginResponse struct {
	User         *UserResponse `json:"user"`
	AccessToken  string        `json:"access_token"`
	RefreshToken string        `json:"refresh_token"`
	ExpiresIn    int64         `json:"expires_in"`
}

// RefreshRequest represents a token refresh request
type RefreshRequest struct {
	RefreshToken string `json:"refresh_token" binding:"required"`
}

// RefreshResponse represents a token refresh response
type RefreshResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
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

// Register handles user registration
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

	// Register user
	user, err := h.userService.Register(c.Request.Context(), req.Email, req.Password)
	if err != nil {
		status := http.StatusInternalServerError
		code := "internal_error"
		errorMsg := "Failed to register user"

		if strings.Contains(err.Error(), "already exists") {
			status = http.StatusConflict
			code = "email_already_exists"
			errorMsg = "User with this email already exists"
		} else if strings.Contains(err.Error(), "password") {
			status = http.StatusBadRequest
			code = "validation_error"
			errorMsg = err.Error()
		}

		c.JSON(status, gin.H{
			"code":  code,
			"error": errorMsg,
		})
		return
	}

	// TEMPORARY: Skip token generation for debugging
	response := RegisterResponse{
		User: &UserResponse{
			ID:    user.ID,
			Email: user.Email,
			Role:  user.Role,
		},
		AccessToken:  "temp-access-token",
		RefreshToken: "temp-refresh-token",
		ExpiresIn:    900,
	}

	c.JSON(http.StatusOK, gin.H{"data": response})
}

// Login handles user authentication
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

	// Authenticate user
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

		c.JSON(status, gin.H{
			"code":  code,
			"error": errorMsg,
		})
		return
	}

	// Generate tokens
	tokenPair, err := h.tokenService.GenerateTokens(c.Request.Context(), user)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code":   "token_generation_error",
			"error":  "Failed to generate tokens",
			"detail": err.Error(),
		})
		return
	}

	response := LoginResponse{
		User: &UserResponse{
			ID:    user.ID,
			Email: user.Email,
			Role:  user.Role,
		},
		AccessToken:  tokenPair.AccessToken,
		RefreshToken: tokenPair.RefreshToken,
		ExpiresIn:    tokenPair.ExpiresIn,
	}

	c.JSON(http.StatusOK, gin.H{"data": response})
}

// Refresh handles token refresh
func (h *AuthHandler) Refresh(c *gin.Context) {
	var req RefreshRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"code":   "validation_error",
			"error":  "Invalid request data",
			"detail": err.Error(),
		})
		return
	}

	// Validate refresh token
	dbToken, err := h.tokenService.ValidateRefreshToken(c.Request.Context(), req.RefreshToken)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{
			"code":  "invalid_or_revoked_refresh",
			"error": "Invalid or revoked refresh token",
		})
		return
	}

	// Revoke old refresh token if rotate is enabled (we'll implement rotation later)
	// For now, just generate new tokens
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
		// Log error but don't fail the request
		c.Error(err)
	}

	response := RefreshResponse{
		AccessToken:  tokenPair.AccessToken,
		RefreshToken: tokenPair.RefreshToken,
		ExpiresIn:    tokenPair.ExpiresIn,
	}

	c.JSON(http.StatusOK, gin.H{"data": response})
}

// Logout handles user logout
func (h *AuthHandler) Logout(c *gin.Context) {
	var req LogoutRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		// Optional request body, continue
		req = LogoutRequest{}
	}

	// Get user from context (set by AuthJWT middleware)
	userID, exists := c.Get("userID")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{
			"code":  "unauthorized",
			"error": "Authentication required",
		})
		return
	}

	// If specific refresh token provided, revoke only that one
	if req.RefreshToken != "" {
		// Validate and revoke specific token
		dbToken, err := h.tokenService.ValidateRefreshToken(c.Request.Context(), req.RefreshToken)
		if err == nil && dbToken != nil && dbToken.UserID == userID.(uint) {
			h.tokenService.RevokeRefreshToken(c.Request.Context(), dbToken.JTI)
		}
	} else {
		// Revoke all user's tokens
		h.tokenService.RevokeAllUserTokens(c.Request.Context(), userID.(uint))
	}

	c.JSON(http.StatusNoContent, nil)
}
