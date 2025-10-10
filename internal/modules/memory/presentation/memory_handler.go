package handlers

import (
	"net/http"
	"strconv"

	"github.com/charmbracelet/log"
	"github.com/gin-gonic/gin"

	memoryservice "system-stats/internal/modules/memory/application"
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

// MemoryHandler handles HTTP requests for memory metrics.
type MemoryHandler struct {
	logger  *log.Logger
	service memoryservice.Service
}

// NewMemoryHandler creates a new HTTP handler for memory metrics endpoints.
func NewMemoryHandler(logger *log.Logger, service memoryservice.Service) *MemoryHandler {
	return &MemoryHandler{
		logger:  logger,
		service: service,
	}
}

// HandleMemoryStats returns current memory metrics with latest and historical data.
func (h *MemoryHandler) HandleMemoryStats(c *gin.Context) {
	h.logger.Info("Handling memory stats request", "client_ip", c.ClientIP(), "user_agent", c.GetHeader("User-Agent"))

	hours := parseHoursQuery(c)
	hostId := parseHostIdQuery(c)

	// Get latest memory metrics from database
	latestMetrics, err := h.service.GetLatest(c.Request.Context())
	if err != nil {
		h.logger.Error("Failed to fetch latest memory metrics", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Get historical memory metrics (filtered by host_id if provided)
	var historyMetrics []interface{}
	if hostId > 0 {
		historyMetrics, err = h.service.GetHistoricalByHost(c.Request.Context(), hostId, hours)
	} else {
		historyMetrics, err = h.service.GetHistorical(c.Request.Context(), hours)
	}
	if err != nil {
		h.logger.Error("Failed to fetch historical memory metrics", "error", err, "hours", hours, "host_id", hostId)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	h.logger.Info("Memory stats response sent successfully", "history_points", len(historyMetrics), "host_id", hostId)
	c.JSON(http.StatusOK, gin.H{
		"latest":  latestMetrics,
		"history": historyMetrics,
	})
}

// HandleMemoryHistory returns memory-specific historical metrics for the requested time range.
func (h *MemoryHandler) HandleMemoryHistory(c *gin.Context) {
	hours := parseHoursQuery(c)
	h.logger.Info("Handling memory history request", "client_ip", c.ClientIP(), "hours", hours)
	history, handled := h.fetchHistory(c, hours)
	if handled {
		return
	}
	h.logger.Info("Memory history response sent successfully", "hours", hours)
	c.JSON(http.StatusOK, gin.H{"memory": history})
}

// fetchHistory loads historical metrics and writes an error response if needed.
func (h *MemoryHandler) fetchHistory(c *gin.Context, hours float64) ([]interface{}, bool) {
	h.logger.Info("Fetching memory historical metrics", "hours", hours)
	history, err := h.service.GetHistorical(c.Request.Context(), hours)
	if err != nil {
		h.logger.Error("Failed to fetch memory historical metrics", "error", err, "hours", hours)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return nil, true
	}
	h.logger.Info("Memory historical metrics fetched successfully", "data_points", len(history))
	return history, false
}
