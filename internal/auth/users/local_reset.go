package users

import (
	"net"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// LocalResetHeader must accompany a loopback reset request (belt and braces
// on top of the RemoteAddr check).
const LocalResetHeader = "X-Node-Stats-Local"

// LocalResetRequest is the body of POST /auth/local-reset.
type LocalResetRequest struct {
	Email       string `json:"email" binding:"required"`
	NewPassword string `json:"new_password" binding:"required"`
}

// LocalReset resets a user's password WITHOUT authentication — accepted only
// from the loopback interface (i.e. `node-stats reset-password …` run on the
// machine itself / `docker exec`). It is the break-glass path for a locked-out
// admin and, unlike editing the DB by hand, goes through the normal service
// (bcrypt + Raft replication), so the change survives restarts.
//
// @Summary     Reset a password from the local machine
// @Description Loopback-only, unauthenticated. Use via `node-stats reset-password <email>`.
// @Tags        auth
// @Accept      json
// @Produce     json
// @Success     200 {object} map[string]string
// @Failure     403 {object} map[string]string
// @Router      /auth/local-reset [post]
func (h *AuthHandler) LocalReset(c *gin.Context) {
	if !isLoopback(c.Request.RemoteAddr) || c.GetHeader(LocalResetHeader) != "1" {
		c.JSON(http.StatusForbidden, gin.H{"code": "forbidden", "error": "local reset is only accepted from the machine itself"})
		return
	}
	var req LocalResetRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": "validation_error", "error": "email and new_password are required"})
		return
	}
	user, err := h.userService.GetByEmail(c.Request.Context(), req.Email)
	if err != nil || user == nil {
		c.JSON(http.StatusNotFound, gin.H{"code": "not_found", "error": "no user with that e-mail"})
		return
	}
	if err := h.userService.ResetPassword(c.Request.Context(), user.ID, req.NewPassword); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": "reset_failed", "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok", "email": user.Email})
}

func isLoopback(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(strings.TrimSpace(remoteAddr))
	if err != nil {
		host = remoteAddr
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}
