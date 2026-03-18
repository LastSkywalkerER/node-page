package handlers

import (
	"net/http"

	"github.com/charmbracelet/log"
	"github.com/gin-gonic/gin"

	hostservice "system-stats/internal/modules/hosts/application"
)

// HostHandler handles HTTP requests for host information.
type HostHandler struct {
	logger  *log.Logger
	service hostservice.Service
}

// NewHostHandler creates a new HTTP handler for host endpoints.
func NewHostHandler(logger *log.Logger, service hostservice.Service) *HostHandler {
	return &HostHandler{
		logger:  logger,
		service: service,
	}
}

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
func (h *HostHandler) HandleRegisterCurrentHost(c *gin.Context) {
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
func (h *HostHandler) HandleGetCurrentHost(c *gin.Context) {
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
func (h *HostHandler) HandleGetAllHosts(c *gin.Context) {
	h.logger.Debug("Handling get all hosts request", "client_ip", c.ClientIP(), "user_agent", c.GetHeader("User-Agent"))

	hosts, err := h.service.GetAllHosts(c.Request.Context())
	if err != nil {
		h.logger.Error("Failed to get all hosts", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	h.logger.Debug("All hosts information retrieved successfully", "count", len(hosts))
	c.JSON(http.StatusOK, gin.H{
		"hosts": hosts,
	})
}
