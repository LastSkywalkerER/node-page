package presentation

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	invservice "system-stats/internal/modules/invitations/application"
)

// InvitationHandler handles invitation-related HTTP requests.
type InvitationHandler struct {
	invService invservice.Service
}

// NewInvitationHandler creates a new invitation handler.
func NewInvitationHandler(invService invservice.Service) *InvitationHandler {
	return &InvitationHandler{invService: invService}
}

// CreateInvitationRequest is the request body for creating an invitation.
type CreateInvitationRequest struct {
	Email string `json:"email" binding:"required,email"`
}

// CreateInvitation creates a new user invitation and returns the link.
//
// @Summary     Create invitation
// @Description Creates a one-time invitation link for user registration. Admin only.
// @Tags        invitations
// @Produce     json
// @Success     200  {object} map[string]interface{}
// @Failure     401  {object} map[string]string
// @Failure     403  {object} map[string]string
// @Failure     500  {object} map[string]string
// @Security    BearerAuth
// @Router      /invitations [post]
func (h *InvitationHandler) CreateInvitation(c *gin.Context) {
	userID, exists := c.Get("userID")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{
			"code":  "unauthorized",
			"error": "Authentication required",
		})
		return
	}
	adminID := userID.(uint)

	var req CreateInvitationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"code":   "validation_error",
			"error":  "Email is required and must be valid",
			"detail": err.Error(),
		})
		return
	}

	// Build base URL from request (scheme + host)
	scheme := "https"
	if c.Request.TLS == nil && strings.HasPrefix(c.Request.Proto, "HTTP/") {
		scheme = "http"
	}
	if s := c.GetHeader("X-Forwarded-Proto"); s != "" {
		scheme = s
	}
	host := c.Request.Host
	if h := c.GetHeader("X-Forwarded-Host"); h != "" {
		host = h
	}
	baseURL := scheme + "://" + host

	token, link, err := h.invService.CreateInvitation(c.Request.Context(), adminID, baseURL, strings.TrimSpace(strings.ToLower(req.Email)))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code":  "internal_error",
			"error": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"data": gin.H{
			"token":      token,
			"link":       link,
			"expires_at": nil, // One-time use, no expiry
		},
	})
}

// ValidateInvitation validates an invite token and returns the expected email (public, no auth).
//
// @Summary     Validate invitation token
// @Description Returns the email this invitation is for. Used to pre-fill registration form.
// @Tags        invitations
// @Param       token  query  string  true  "Invitation token from URL"
// @Produce     json
// @Success     200  {object} map[string]interface{}
// @Failure     400  {object} map[string]string
// @Router      /invitations/validate [get]
func (h *InvitationHandler) ValidateInvitation(c *gin.Context) {
	token := c.Query("token")
	if token == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"code":  "validation_error",
			"error": "Token is required",
		})
		return
	}

	inv, err := h.invService.ValidateToken(c.Request.Context(), token)
	if err != nil || inv == nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"code":  "invalid_invitation",
			"error": "Invalid or already used invitation link",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"data": gin.H{
			"email": inv.Email,
		},
	})
}
