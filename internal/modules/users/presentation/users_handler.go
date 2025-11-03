package presentation

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	userservice "system-stats/internal/modules/users/application"
)

// UsersHandler handles user management HTTP requests
type UsersHandler struct {
	userService userservice.UserService
}

// NewUsersHandler creates a new users handler
func NewUsersHandler(userService userservice.UserService) *UsersHandler {
	return &UsersHandler{
		userService: userService,
	}
}

// Me returns the current authenticated user
func (h *UsersHandler) Me(c *gin.Context) {
	userID, exists := c.Get("userID")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{
			"code":  "unauthorized",
			"error": "Authentication required",
		})
		return
	}

	user, err := h.userService.GetByID(c.Request.Context(), userID.(uint))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code":  "internal_error",
			"error": "Failed to get user",
		})
		return
	}
	if user == nil {
		c.JSON(http.StatusNotFound, gin.H{
			"code":  "user_not_found",
			"error": "User not found",
		})
		return
	}

	response := UserResponse{
		ID:    user.ID,
		Email: user.Email,
		Role:  user.Role,
	}

	c.JSON(http.StatusOK, gin.H{"data": response})
}

// List returns a paginated list of users (admin only)
func (h *UsersHandler) List(c *gin.Context) {
	// Parse pagination parameters
	offsetStr := c.DefaultQuery("offset", "0")
	limitStr := c.DefaultQuery("limit", "20")

	offset, err := strconv.Atoi(offsetStr)
	if err != nil || offset < 0 {
		offset = 0
	}

	limit, err := strconv.Atoi(limitStr)
	if err != nil || limit < 1 || limit > 100 {
		limit = 20
	}

	users, err := h.userService.List(c.Request.Context(), offset, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code":  "internal_error",
			"error": "Failed to list users",
		})
		return
	}

	// Convert to response format
	userResponses := make([]UserResponse, len(users))
	for i, user := range users {
		userResponses[i] = UserResponse{
			ID:    user.ID,
			Email: user.Email,
			Role:  user.Role,
		}
	}

	// Get total count for pagination info
	total, err := h.userService.Count(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code":  "internal_error",
			"error": "Failed to count users",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"data": userResponses,
		"meta": gin.H{
			"total":  total,
			"offset": offset,
			"limit":  limit,
		},
	})
}

// UpdateRoleRequest represents a role update request
type UpdateRoleRequest struct {
	Role string `json:"role" binding:"required,oneof=ADMIN USER"`
}

// UpdateRole updates a user's role (admin only)
func (h *UsersHandler) UpdateRole(c *gin.Context) {
	userIDStr := c.Param("id")
	userID, err := strconv.ParseUint(userIDStr, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"code":  "invalid_user_id",
			"error": "Invalid user ID",
		})
		return
	}

	var req UpdateRoleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"code":   "validation_error",
			"error":  "Invalid request data",
			"detail": err.Error(),
		})
		return
	}

	// Check if user exists
	user, err := h.userService.GetByID(c.Request.Context(), uint(userID))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code":  "internal_error",
			"error": "Failed to get user",
		})
		return
	}
	if user == nil {
		c.JSON(http.StatusNotFound, gin.H{
			"code":  "user_not_found",
			"error": "User not found",
		})
		return
	}

	// Update role
	if err := h.userService.UpdateRole(c.Request.Context(), uint(userID), req.Role); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code":  "internal_error",
			"error": "Failed to update user role",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"data": gin.H{
			"id":    userID,
			"email": user.Email,
			"role":  req.Role,
		},
	})
}

// Delete deletes a user (admin only)
func (h *UsersHandler) Delete(c *gin.Context) {
	userIDStr := c.Param("id")
	userID, err := strconv.ParseUint(userIDStr, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"code":  "invalid_user_id",
			"error": "Invalid user ID",
		})
		return
	}

	// Get current user ID to prevent self-deletion
	currentUserID, exists := c.Get("userID")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{
			"code":  "unauthorized",
			"error": "Authentication required",
		})
		return
	}

	// Prevent self-deletion
	if currentUserID.(uint) == uint(userID) {
		c.JSON(http.StatusBadRequest, gin.H{
			"code":  "self_deletion_not_allowed",
			"error": "Cannot delete your own account",
		})
		return
	}

	// Check if user exists
	user, err := h.userService.GetByID(c.Request.Context(), uint(userID))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code":  "internal_error",
			"error": "Failed to get user",
		})
		return
	}
	if user == nil {
		c.JSON(http.StatusNotFound, gin.H{
			"code":  "user_not_found",
			"error": "User not found",
		})
		return
	}

	// Delete user
	if err := h.userService.Delete(c.Request.Context(), uint(userID)); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code":  "internal_error",
			"error": "Failed to delete user",
		})
		return
	}

	// Note: Token revocation should be handled by the service layer
	// This would require passing TokenService to UsersHandler or handling in UserService.Delete

	c.JSON(http.StatusNoContent, nil)
}
