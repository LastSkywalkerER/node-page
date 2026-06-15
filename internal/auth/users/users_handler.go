package users

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

// UsersHandler handles user management HTTP requests
type UsersHandler struct {
	userService UserService
}

// NewUsersHandler creates a new users handler
func NewUsersHandler(userService UserService) *UsersHandler {
	return &UsersHandler{
		userService: userService,
	}
}

// Me returns the current authenticated user
//
// @Summary     Current user
// @Description Returns the profile of the currently authenticated user.
// @Tags        users
// @Produce     json
// @Success     200  {object} map[string]interface{}
// @Failure     401  {object} map[string]string
// @Failure     500  {object} map[string]string
// @Security    BearerAuth
// @Router      /users/me [get]
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
//
// @Summary     List users
// @Description Returns a paginated list of all users. Admin role required.
// @Tags        users
// @Produce     json
// @Param       offset  query    integer  false  "Pagination offset"  default(0)
// @Param       limit   query    integer  false  "Page size (max 100)"  default(20)
// @Success     200     {object} map[string]interface{}
// @Failure     401     {object} map[string]string
// @Failure     403     {object} map[string]string
// @Failure     500     {object} map[string]string
// @Security    BearerAuth
// @Router      /users [get]
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
//
// @Summary     Update user role
// @Description Changes the role of a user (ADMIN or USER). Admin role required.
// @Tags        users
// @Accept      json
// @Produce     json
// @Param       id    path      integer           true  "User ID"
// @Param       body  body      UpdateRoleRequest true  "New role"
// @Success     200   {object}  map[string]interface{}
// @Failure     400   {object}  map[string]string
// @Failure     401   {object}  map[string]string
// @Failure     403   {object}  map[string]string
// @Failure     404   {object}  map[string]string
// @Failure     500   {object}  map[string]string
// @Security    BearerAuth
// @Router      /users/{id} [patch]
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

	currentUserID, exists := c.Get("userID")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"code": "unauthorized", "error": "Authentication required"})
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

	// Update role (service enforces self-demote and last-admin guards)
	if err := h.userService.UpdateRole(c.Request.Context(), currentUserID.(uint), uint(userID), req.Role); err != nil {
		switch {
		case errors.Is(err, ErrCannotDemoteSelf):
			c.JSON(http.StatusBadRequest, gin.H{"code": "cannot_demote_self", "error": "Cannot demote yourself"})
		case errors.Is(err, ErrCannotDemoteLastAdmin):
			c.JSON(http.StatusBadRequest, gin.H{"code": "cannot_demote_last_admin", "error": "Cannot demote the last admin"})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"code": "internal_error", "error": "Failed to update user role"})
		}
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

// ChangePasswordRequest is the body of POST /users/me/password.
type ChangePasswordRequest struct {
	CurrentPassword string `json:"current_password" binding:"required"`
	NewPassword     string `json:"new_password" binding:"required"`
}

// ChangeOwnPassword lets the authenticated user change their own password.
//
// @Summary     Change own password
// @Tags        users
// @Accept      json
// @Produce     json
// @Param       body  body      ChangePasswordRequest true  "Current + new password"
// @Success     200   {object}  map[string]interface{}
// @Failure     400   {object}  map[string]string
// @Failure     401   {object}  map[string]string
// @Security    BearerAuth
// @Router      /users/me/password [post]
func (h *UsersHandler) ChangeOwnPassword(c *gin.Context) {
	userID, exists := c.Get("userID")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"code": "unauthorized", "error": "Authentication required"})
		return
	}

	var req ChangePasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": "validation_error", "error": "Invalid request data", "detail": err.Error()})
		return
	}

	err := h.userService.ChangePassword(c.Request.Context(), userID.(uint), req.CurrentPassword, req.NewPassword)
	switch {
	case err == nil:
		c.JSON(http.StatusOK, gin.H{"data": gin.H{"message": "Password changed. Please sign in again."}})
	case errors.Is(err, ErrWrongCurrentPassword):
		c.JSON(http.StatusBadRequest, gin.H{"code": "wrong_current_password", "error": "Current password is incorrect"})
	case errors.Is(err, ErrWeakPassword):
		c.JSON(http.StatusBadRequest, gin.H{"code": "weak_password", "error": ErrWeakPassword.Error()})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"code": "internal_error", "error": "Failed to change password"})
	}
}

// ResetPasswordRequest is the body of POST /users/:id/reset-password.
type ResetPasswordRequest struct {
	NewPassword string `json:"new_password" binding:"required"`
}

// ResetUserPassword lets an admin set another user's password (no current password).
//
// @Summary     Reset a user's password (admin)
// @Tags        users
// @Accept      json
// @Produce     json
// @Param       id    path      integer              true  "User ID"
// @Param       body  body      ResetPasswordRequest true  "New password"
// @Success     200   {object}  map[string]interface{}
// @Failure     400   {object}  map[string]string
// @Failure     404   {object}  map[string]string
// @Security    BearerAuth
// @Router      /users/{id}/reset-password [post]
func (h *UsersHandler) ResetUserPassword(c *gin.Context) {
	userIDStr := c.Param("id")
	userID, err := strconv.ParseUint(userIDStr, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": "invalid_user_id", "error": "Invalid user ID"})
		return
	}

	var req ResetPasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": "validation_error", "error": "Invalid request data", "detail": err.Error()})
		return
	}

	err = h.userService.ResetPassword(c.Request.Context(), uint(userID), req.NewPassword)
	switch {
	case err == nil:
		c.JSON(http.StatusOK, gin.H{"data": gin.H{"id": userID, "message": "Password reset"}})
	case errors.Is(err, ErrInvalidCredentials):
		c.JSON(http.StatusNotFound, gin.H{"code": "user_not_found", "error": "User not found"})
	case errors.Is(err, ErrWeakPassword):
		c.JSON(http.StatusBadRequest, gin.H{"code": "weak_password", "error": ErrWeakPassword.Error()})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"code": "internal_error", "error": "Failed to reset password"})
	}
}

// Delete deletes a user (admin only)
//
// @Summary     Delete user
// @Description Permanently deletes a user account. Cannot delete your own account. Admin role required.
// @Tags        users
// @Produce     json
// @Param       id   path     integer  true  "User ID"
// @Success     204  "No Content"
// @Failure     400  {object} map[string]string
// @Failure     401  {object} map[string]string
// @Failure     403  {object} map[string]string
// @Failure     404  {object} map[string]string
// @Failure     500  {object} map[string]string
// @Security    BearerAuth
// @Router      /users/{id} [delete]
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

	// Delete user (service enforces last-admin guard and revokes refresh tokens)
	if err := h.userService.Delete(c.Request.Context(), currentUserID.(uint), uint(userID)); err != nil {
		if errors.Is(err, ErrCannotDeleteLastAdmin) {
			c.JSON(http.StatusBadRequest, gin.H{"code": "cannot_delete_last_admin", "error": "Cannot delete the last admin"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{
			"code":  "internal_error",
			"error": "Failed to delete user",
		})
		return
	}

	c.JSON(http.StatusNoContent, nil)
}
