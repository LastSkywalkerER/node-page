package handlers

import (
	"net/http"
	"strconv"

	"github.com/charmbracelet/log"
	"github.com/gin-gonic/gin"

	cpuservice "system-stats/internal/modules/cpu/application"
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

// CPUHandler handles HTTP requests for CPU metrics.
type CPUHandler struct {
	logger  *log.Logger
	service cpuservice.Service
}

// NewCPUHandler creates a new HTTP handler for CPU metrics endpoints.
func NewCPUHandler(logger *log.Logger, service cpuservice.Service) *CPUHandler {
	return &CPUHandler{
		logger:  logger,
		service: service,
	}
}

// HandleCPUStats returns current CPU metrics with latest and historical data.
func (h *CPUHandler) HandleCPUStats(c *gin.Context) {
	h.logger.Info("Handling CPU stats request", "client_ip", c.ClientIP(), "user_agent", c.GetHeader("User-Agent"))

	hours := parseHoursQuery(c)
	hostId := parseHostIdQuery(c)

	h.logger.Info("CPU stats request parameters", "hours", hours, "host_id", hostId, "host_id_str", c.Query("host_id"))

	// Get latest CPU metrics from database
	latestMetrics, err := h.service.GetLatest(c.Request.Context())
	if err != nil {
		h.logger.Error("Failed to fetch latest CPU metrics", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Get historical CPU metrics (filtered by host_id if provided)
	var historyMetrics []interface{}
	if hostId > 0 {
		historyMetrics, err = h.service.GetHistoricalByHost(c.Request.Context(), hostId, hours)
	} else {
		historyMetrics, err = h.service.GetHistorical(c.Request.Context(), hours)
	}
	if err != nil {
		h.logger.Error("Failed to fetch historical CPU metrics", "error", err, "hours", hours, "host_id", hostId)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	h.logger.Info("CPU stats response sent successfully", "history_points", len(historyMetrics), "host_id", hostId)
	c.JSON(http.StatusOK, gin.H{
		"latest":  latestMetrics,
		"history": historyMetrics,
	})
}

// HandleCPUHistory returns CPU-specific historical metrics for the requested time range.
func (h *CPUHandler) HandleCPUHistory(c *gin.Context) {
	hours := parseHoursQuery(c)
	h.logger.Info("Handling CPU history request", "client_ip", c.ClientIP(), "hours", hours)
	history, handled := h.fetchHistory(c, hours)
	if handled {
		return
	}
	h.logger.Info("CPU history response sent successfully", "hours", hours)
	c.JSON(http.StatusOK, gin.H{"cpu": history})
}

// fetchHistory loads historical metrics and writes an error response if needed.
func (h *CPUHandler) fetchHistory(c *gin.Context, hours float64) ([]interface{}, bool) {
	h.logger.Info("Fetching CPU historical metrics", "hours", hours)
	history, err := h.service.GetHistorical(c.Request.Context(), hours)
	if err != nil {
		h.logger.Error("Failed to fetch CPU historical metrics", "error", err, "hours", hours)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return nil, true
	}
	h.logger.Info("CPU historical metrics fetched successfully", "data_points", len(history))
	return history, false
}
