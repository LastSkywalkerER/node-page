package handlers

import (
	"net/http"
	"strconv"

	"github.com/charmbracelet/log"
	"github.com/gin-gonic/gin"

	memoryservice "system-stats/internal/modules/memory/application"
	memoryentities "system-stats/internal/modules/memory/infrastructure/entities"
)

func parseHoursQuery(c *gin.Context) float64 {
	hoursStr := c.DefaultQuery("hours", "0.0833")
	hours, err := strconv.ParseFloat(hoursStr, 64)
	if err != nil {
		return 0.0833
	}
	return hours
}

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
	hours := parseHoursQuery(c)
	hostId := parseHostIdQuery(c)

	latestMetrics, err := h.service.GetLatest(c.Request.Context())
	if err != nil {
		h.logger.Error("Failed to fetch latest memory metrics", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	var historyMetrics []memoryentities.HistoricalMemoryMetric
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

	c.JSON(http.StatusOK, gin.H{
		"latest":  latestMetrics,
		"history": historyMetrics,
	})
}
