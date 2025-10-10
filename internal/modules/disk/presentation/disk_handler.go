package handlers

import (
	"net/http"
	"strconv"

	"github.com/charmbracelet/log"
	"github.com/gin-gonic/gin"

	diskservice "system-stats/internal/modules/disk/application"
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

// DiskHandler handles HTTP requests for disk metrics.
type DiskHandler struct {
	logger  *log.Logger
	service diskservice.Service
}

// NewDiskHandler creates a new HTTP handler for disk metrics endpoints.
func NewDiskHandler(logger *log.Logger, service diskservice.Service) *DiskHandler {
	return &DiskHandler{
		logger:  logger,
		service: service,
	}
}

// HandleDiskStats returns current disk metrics with latest and historical data.
func (h *DiskHandler) HandleDiskStats(c *gin.Context) {
	h.logger.Info("Handling disk stats request", "client_ip", c.ClientIP(), "user_agent", c.GetHeader("User-Agent"))

	hours := parseHoursQuery(c)
	hostId := parseHostIdQuery(c)

	// Get latest disk metrics from database
	latestMetrics, err := h.service.GetLatest(c.Request.Context())
	if err != nil {
		h.logger.Error("Failed to fetch latest disk metrics", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Get historical disk metrics (filtered by host_id if provided)
	var historyMetrics []interface{}
	if hostId > 0 {
		historyMetrics, err = h.service.GetHistoricalByHost(c.Request.Context(), hostId, hours)
	} else {
		historyMetrics, err = h.service.GetHistorical(c.Request.Context(), hours)
	}
	if err != nil {
		h.logger.Error("Failed to fetch historical disk metrics", "error", err, "hours", hours, "host_id", hostId)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	h.logger.Info("Disk stats response sent successfully", "history_points", len(historyMetrics), "host_id", hostId)
	c.JSON(http.StatusOK, gin.H{
		"latest":  latestMetrics,
		"history": historyMetrics,
	})
}

// HandleDiskHistory returns disk-specific historical metrics for the requested time range.
func (h *DiskHandler) HandleDiskHistory(c *gin.Context) {
	hours := parseHoursQuery(c)
	h.logger.Info("Handling disk history request", "client_ip", c.ClientIP(), "hours", hours)
	history, handled := h.fetchHistory(c, hours)
	if handled {
		return
	}
	h.logger.Info("Disk history response sent successfully", "hours", hours)
	c.JSON(http.StatusOK, gin.H{"disk": history})
}

// fetchHistory loads historical metrics and writes an error response if needed.
func (h *DiskHandler) fetchHistory(c *gin.Context, hours float64) ([]interface{}, bool) {
	h.logger.Info("Fetching disk historical metrics", "hours", hours)
	history, err := h.service.GetHistorical(c.Request.Context(), hours)
	if err != nil {
		h.logger.Error("Failed to fetch disk historical metrics", "error", err, "hours", hours)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return nil, true
	}
	h.logger.Info("Disk historical metrics fetched successfully", "data_points", len(history))
	return history, false
}
