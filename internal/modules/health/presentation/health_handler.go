package handlers

import (
	"net/http"
	"strconv"

	"github.com/charmbracelet/log"
	"github.com/gin-gonic/gin"

	healthservice "system-stats/internal/modules/health/application"
)

// HealthHandler handles HTTP requests for health checks.
type HealthHandler struct {
	logger  *log.Logger
	service healthservice.Service
}

// NewHealthHandler creates a new HTTP handler for health endpoints.
func NewHealthHandler(logger *log.Logger, service healthservice.Service) *HealthHandler {
	return &HealthHandler{
		logger:  logger,
		service: service,
	}
}

// HandleHealth returns health check information.
func (h *HealthHandler) HandleHealth(c *gin.Context) {
	h.logger.Info("Handling health check request", "client_ip", c.ClientIP(), "user_agent", c.GetHeader("User-Agent"))

	// Parse optional host_id query parameter
	var hostID *uint
	hostIDStr := c.Query("host_id")
	if hostIDStr != "" {
		if id, err := strconv.ParseUint(hostIDStr, 10, 32); err == nil {
			hostIDUint := uint(id)
			hostID = &hostIDUint
		} else {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid host_id parameter"})
			return
		}
	}

	health, err := h.service.GetHealth(c.Request.Context(), hostID)
	if err != nil {
		h.logger.Error("Failed to get health information", "error", err, "host_id", hostID)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	h.logger.Info("Health information retrieved successfully", "host_id", hostID, "status", health.Status)
	c.JSON(http.StatusOK, health)
}
