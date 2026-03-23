package handlers

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/charmbracelet/log"
	"github.com/gin-gonic/gin"

	"system-stats/internal/app/metricshost"
	hostservice "system-stats/internal/modules/hosts/application"
	memoryservice "system-stats/internal/modules/memory/application"
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
	hosts   hostservice.Service
}

// NewMemoryHandler creates a new HTTP handler for memory metrics endpoints.
func NewMemoryHandler(logger *log.Logger, service memoryservice.Service, hosts hostservice.Service) *MemoryHandler {
	return &MemoryHandler{
		logger:  logger,
		service: service,
		hosts:   hosts,
	}
}

// HandleMemoryStats returns current memory metrics with latest and historical data.
//
// @Summary     Memory metrics
// @Description Returns latest RAM snapshot and historical usage data.
// @Tags        metrics
// @Produce     json
// @Param       hours    query    number   false  "History window in hours"  default(0.0833)
// @Param       host_id  query    integer  false  "Host ID (0 = this server instance)"
// @Success     200      {object} map[string]interface{}
// @Failure     401      {object} map[string]string
// @Failure     500      {object} map[string]string
// @Security    BearerAuth
// @Router      /memory [get]
func (h *MemoryHandler) HandleMemoryStats(c *gin.Context) {
	hours := parseHoursQuery(c)
	queryHost := parseHostIdQuery(c)

	effective, err := metricshost.EffectiveHostID(c.Request.Context(), h.hosts, queryHost)
	if errors.Is(err, metricshost.ErrHostNotFound) {
		c.JSON(http.StatusOK, metricshost.EmptyMemoryPayload())
		return
	}
	if err != nil {
		h.logger.Error("Failed to resolve host for memory metrics", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	latestMetrics, err := h.service.GetLatestByHost(c.Request.Context(), effective)
	if err != nil {
		h.logger.Error("Failed to fetch latest memory metrics", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	historyMetrics, err := h.service.GetHistoricalByHost(c.Request.Context(), effective, hours)
	if err != nil {
		h.logger.Error("Failed to fetch historical memory metrics", "error", err, "hours", hours, "host_id", effective)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"latest":  latestMetrics,
		"history": historyMetrics,
	})
}
