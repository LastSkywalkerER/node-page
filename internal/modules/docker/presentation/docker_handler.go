package handlers

import (
	"net/http"
	"strconv"

	"github.com/charmbracelet/log"
	"github.com/gin-gonic/gin"

	dockerservice "system-stats/internal/modules/docker/application"
)

// parseHoursQuery parses the 'hours' query parameter from the request.
func parseHoursQuery(c *gin.Context) float64 {
	// Default to 5 minutes (5/60 hours)
	hoursStr := c.DefaultQuery("hours", "0.0833")
	hours, err := strconv.ParseFloat(hoursStr, 64)
	if err != nil {
		return 0.0833
	}
	return hours
}

// parseHostIdQuery parses the 'host_id' query parameter from the request.
func parseHostIdQuery(c *gin.Context) uint {
	hostIdStr := c.DefaultQuery("host_id", "0")
	hostId, err := strconv.ParseUint(hostIdStr, 10, 32)
	if err != nil {
		return 0
	}
	return uint(hostId)
}

// DockerHandler handles HTTP requests for Docker container metrics.
type DockerHandler struct {
	logger  *log.Logger
	service dockerservice.Service
}

// NewDockerHandler creates a new HTTP handler for Docker metrics endpoints.
func NewDockerHandler(logger *log.Logger, service dockerservice.Service) *DockerHandler {
	return &DockerHandler{
		logger:  logger,
		service: service,
	}
}

// HandleDockerStats returns Docker container statistics and status information with latest and historical data.
func (h *DockerHandler) HandleDockerStats(c *gin.Context) {
	h.logger.Info("Handling Docker stats request", "client_ip", c.ClientIP(), "user_agent", c.GetHeader("User-Agent"))

	hours := parseHoursQuery(c)
	hostId := parseHostIdQuery(c)

	// Get latest Docker metrics from database
	latestMetrics, err := h.service.GetLatest(c.Request.Context())
	if err != nil {
		h.logger.Error("Failed to fetch latest Docker metrics", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":            err.Error(),
			"docker_available": false,
		})
		return
	}

	// Get historical Docker metrics (filtered by host_id if provided)
	var historyMetrics []interface{}
	if hostId > 0 {
		historyMetrics, err = h.service.GetHistoricalByHost(c.Request.Context(), hostId, hours)
	} else {
		historyMetrics, err = h.service.GetHistorical(c.Request.Context(), hours)
	}
	if err != nil {
		h.logger.Error("Failed to fetch historical Docker metrics", "error", err, "hours", hours, "host_id", hostId)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":            err.Error(),
			"docker_available": false,
		})
		return
	}

	h.logger.Info("Docker stats response sent successfully", "total_containers", latestMetrics.TotalContainers, "running_containers", latestMetrics.RunningContainers, "history_points", len(historyMetrics), "host_id", hostId)
	c.JSON(http.StatusOK, gin.H{
		"latest":           latestMetrics,
		"history":          historyMetrics,
		"docker_available": true,
	})
}
