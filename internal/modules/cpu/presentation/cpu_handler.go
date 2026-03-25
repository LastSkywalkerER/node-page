package handlers

import (
	"errors"
	"net/http"

	"github.com/charmbracelet/log"
	"github.com/gin-gonic/gin"

	"system-stats/internal/app/httputil"
	"system-stats/internal/app/metricshost"
	cpuservice "system-stats/internal/modules/cpu/application"
	hostservice "system-stats/internal/modules/hosts/application"
)

// CPUHandler handles HTTP requests for CPU metrics.
type CPUHandler struct {
	logger  *log.Logger
	service cpuservice.Service
	hosts   hostservice.Service
}

// NewCPUHandler creates a new HTTP handler for CPU metrics endpoints.
func NewCPUHandler(logger *log.Logger, service cpuservice.Service, hosts hostservice.Service) *CPUHandler {
	return &CPUHandler{
		logger:  logger,
		service: service,
		hosts:   hosts,
	}
}

// HandleCPUStats returns current CPU metrics with latest and historical data.
//
// @Summary     CPU metrics
// @Description Returns latest CPU snapshot and historical usage data.
// @Tags        metrics
// @Produce     json
// @Param       hours    query    number   false  "History window in hours"  default(0.0833)
// @Param       host_id  query    integer  false  "Host ID (0 = this server instance)"
// @Success     200      {object} map[string]interface{}
// @Failure     401      {object} map[string]string
// @Failure     500      {object} map[string]string
// @Security    BearerAuth
// @Router      /cpu [get]
func (h *CPUHandler) HandleCPUStats(c *gin.Context) {
	hours := httputil.ParseHoursQuery(c)
	queryHost := httputil.ParseHostIdQuery(c)

	effective, err := metricshost.EffectiveHostID(c.Request.Context(), h.hosts, queryHost)
	if errors.Is(err, metricshost.ErrHostNotFound) {
		c.JSON(http.StatusOK, metricshost.EmptyCPUPayload())
		return
	}
	if err != nil {
		h.logger.Error("Failed to resolve host for CPU metrics", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	latestMetrics, err := h.service.GetLatestByHost(c.Request.Context(), effective)
	if err != nil {
		h.logger.Error("Failed to fetch latest CPU metrics", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	historyMetrics, err := h.service.GetHistoricalByHost(c.Request.Context(), effective, hours)
	if err != nil {
		h.logger.Error("Failed to fetch historical CPU metrics", "error", err, "hours", hours, "host_id", effective)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"latest":  latestMetrics,
		"history": historyMetrics,
	})
}
