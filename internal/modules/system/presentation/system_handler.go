package handlers

import (
	"net/http"

	"github.com/charmbracelet/log"
	"github.com/gin-gonic/gin"

	systemsrv "system-stats/internal/modules/system/application"
)

// SystemHandler handles HTTP requests for system metrics dashboard.
type SystemHandler struct {
	logger  *log.Logger
	service systemsrv.Service
}

// NewSystemHandler creates a new HTTP handler for system metrics endpoints.
func NewSystemHandler(logger *log.Logger, service systemsrv.Service) *SystemHandler {
	return &SystemHandler{
		logger:  logger,
		service: service,
	}
}

// HandleCurrentMetrics returns current system metrics for the dashboard.
func (h *SystemHandler) HandleCurrentMetrics(c *gin.Context) {
	h.logger.Info("Handling current metrics JSON request", "client_ip", c.ClientIP())
	metrics, err := h.service.CollectAllCurrent(c.Request.Context())
	if err != nil {
		h.logger.Error("Failed to get current metrics", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	h.logger.Info("Current metrics response sent successfully")
	c.JSON(http.StatusOK, metrics)
}
