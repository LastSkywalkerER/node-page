package hosts

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/charmbracelet/log"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// Handler handles HTTP requests for host information.
type Handler struct {
	logger        *log.Logger
	service       Service
	dashboardURLs DashboardURLSource
}

// NewHandler creates a new HTTP handler for host endpoints.
func NewHandler(logger *log.Logger, service Service) *Handler {
	return &Handler{
		logger:  logger,
		service: service,
	}
}

// DashboardURLSource resolves a host's node-stats dashboard URL (cluster peer
// catalog). Optional — wired from the raft layer via SetDashboardURLSource.
type DashboardURLSource interface {
	DashboardURL(ctx context.Context, h Host) string
}

// SetDashboardURLSource enables dashboard_url enrichment on GET /hosts.
func (h *Handler) SetDashboardURLSource(src DashboardURLSource) { h.dashboardURLs = src }

// HandleRegisterCurrentHost registers or updates the current host information.
//
// @Summary     Register current host
// @Description Registers or updates the current host entry (hostname, OS, uptime, etc.).
// @Tags        hosts
// @Produce     json
// @Success     200  {object} map[string]interface{}
// @Failure     401  {object} map[string]string
// @Failure     500  {object} map[string]string
// @Security    BearerAuth
// @Router      /hosts/register [post]
func (h *Handler) HandleRegisterCurrentHost(c *gin.Context) {
	h.logger.Debug("Handling register current host request", "client_ip", c.ClientIP(), "user_agent", c.GetHeader("User-Agent"))

	host, err := h.service.RegisterOrUpdateCurrentHost(c.Request.Context())
	if err != nil {
		h.logger.Error("Failed to register/update current host", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	h.logger.Debug("Current host registered/updated successfully", "host_id", host.ID)
	c.JSON(http.StatusOK, gin.H{
		"host": host,
	})
}

// HandleGetCurrentHost returns information about the current host.
//
// @Summary     Current host info
// @Description Returns hostname, OS, uptime, and hardware info for the host running this server.
// @Tags        hosts
// @Produce     json
// @Success     200  {object} map[string]interface{}
// @Failure     401  {object} map[string]string
// @Failure     500  {object} map[string]string
// @Security    BearerAuth
// @Router      /hosts/current [get]
func (h *Handler) HandleGetCurrentHost(c *gin.Context) {
	h.logger.Debug("Handling get current host request", "client_ip", c.ClientIP(), "user_agent", c.GetHeader("User-Agent"))

	host, err := h.service.GetCurrentHost(c.Request.Context())
	if err != nil {
		h.logger.Error("Failed to get current host", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	h.logger.Debug("Current host information retrieved successfully", "host_id", host.ID)
	c.JSON(http.StatusOK, gin.H{
		"host": host,
	})
}

// HandleGetAllHosts returns information about all registered hosts.
//
// @Summary     All registered hosts
// @Description Returns the list of all hosts that have registered with this server.
// @Tags        hosts
// @Produce     json
// @Success     200  {object} map[string]interface{}
// @Failure     401  {object} map[string]string
// @Failure     500  {object} map[string]string
// @Security    BearerAuth
// @Router      /hosts [get]
func (h *Handler) HandleGetAllHosts(c *gin.Context) {
	h.logger.Debug("Handling get all hosts request", "client_ip", c.ClientIP(), "user_agent", c.GetHeader("User-Agent"))

	hosts, err := h.service.GetAllHosts(c.Request.Context())
	if err != nil {
		h.logger.Error("Failed to get all hosts", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	if h.dashboardURLs != nil {
		for i := range hosts {
			// Live Raft peer catalog wins; otherwise keep the URL persisted from
			// the host's own metric batch (the only source on a bridged hub).
			if u := h.dashboardURLs.DashboardURL(c.Request.Context(), hosts[i]); u != "" {
				hosts[i].DashboardURL = u
			}
		}
	}

	h.logger.Debug("All hosts information retrieved successfully", "count", len(hosts))
	c.JSON(http.StatusOK, gin.H{
		"hosts": hosts,
	})
}

// HandleDeleteHost removes a registered host and all of its stored metrics.
// When Raft is enabled the removal is replicated to every node (keyed by MAC);
// otherwise it is applied locally. The local collector row (id=1) cannot be
// removed this way — a node leaves the cluster via the Raft "leave" flow.
//
// @Summary     Remove a registered host
// @Description Deletes a host row and all its metrics (cluster-wide when Raft is enabled). Admin only.
// @Tags        hosts
// @Produce     json
// @Param       id   path     integer  true  "Host ID"
// @Success     200  {object} map[string]interface{}
// @Failure     400  {object} map[string]string
// @Failure     401  {object} map[string]string
// @Failure     500  {object} map[string]string
// @Security    BearerAuth
// @Router      /hosts/{id} [delete]
func (h *Handler) HandleDeleteHost(c *gin.Context) {
	id64, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id64 == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid host id"})
		return
	}
	hostID := uint(id64)

	if err := h.service.RemoveHost(c.Request.Context(), hostID); err != nil {
		if errors.Is(err, ErrCannotRemoveLocalHost) {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		h.logger.Error("Failed to remove host", "error", err, "host_id", hostID)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	h.logger.Info("Host removed", "host_id", hostID)
	c.JSON(http.StatusOK, gin.H{"removed": hostID})
}

// HandleListPendingChanges returns every parked host-identity proposal.
//
// @Summary     List pending host identity changes
// @Description Connector-proposed identity updates (rename, MAC change) frozen for admin approval. Admin only.
// @Tags        hosts
// @Produce     json
// @Success     200  {object} map[string]interface{}
// @Failure     401  {object} map[string]string
// @Failure     500  {object} map[string]string
// @Security    BearerAuth
// @Router      /hosts/pending-changes [get]
func (h *Handler) HandleListPendingChanges(c *gin.Context) {
	changes, err := h.service.ListPendingChanges(c.Request.Context())
	if err != nil {
		h.logger.Error("Failed to list pending host changes", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if changes == nil {
		changes = []HostPendingChange{}
	}
	c.JSON(http.StatusOK, gin.H{"changes": changes})
}

// HandleApprovePendingChange applies a parked identity proposal.
//
// @Summary     Approve a pending host identity change
// @Description Applies the proposal onto the host row (cluster-wide when Raft is enabled) and removes it. Admin only.
// @Tags        hosts
// @Produce     json
// @Param       change_id  path  string  true  "Change ID"
// @Success     200  {object} map[string]interface{}
// @Failure     404  {object} map[string]string
// @Failure     500  {object} map[string]string
// @Security    BearerAuth
// @Router      /hosts/pending-changes/{change_id}/approve [post]
func (h *Handler) HandleApprovePendingChange(c *gin.Context) {
	changeID := c.Param("change_id")
	if err := h.service.ApprovePendingChange(c.Request.Context(), changeID); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "pending change not found"})
			return
		}
		h.logger.Error("Failed to approve pending host change", "error", err, "change_id", changeID)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	h.logger.Info("Pending host change approved", "change_id", changeID)
	c.JSON(http.StatusOK, gin.H{"applied": changeID})
}

// HandleRejectPendingChange marks a parked identity proposal rejected.
//
// @Summary     Reject a pending host identity change
// @Description Marks the proposal rejected; the connector stops re-proposing the same value until it changes at the source. Admin only.
// @Tags        hosts
// @Produce     json
// @Param       change_id  path  string  true  "Change ID"
// @Success     200  {object} map[string]interface{}
// @Failure     404  {object} map[string]string
// @Failure     500  {object} map[string]string
// @Security    BearerAuth
// @Router      /hosts/pending-changes/{change_id}/reject [post]
func (h *Handler) HandleRejectPendingChange(c *gin.Context) {
	changeID := c.Param("change_id")
	if err := h.service.RejectPendingChange(c.Request.Context(), changeID); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "pending change not found"})
			return
		}
		h.logger.Error("Failed to reject pending host change", "error", err, "change_id", changeID)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	h.logger.Info("Pending host change rejected", "change_id", changeID)
	c.JSON(http.StatusOK, gin.H{"rejected": changeID})
}
