package handlers

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/charmbracelet/log"
	"github.com/gin-gonic/gin"

	"system-stats/internal/app/metricshost"
	hostservice "system-stats/internal/modules/hosts/application"
	systemsrv "system-stats/internal/modules/system/application"
)

// SystemHandler handles HTTP requests for system metrics dashboard.
type SystemHandler struct {
	logger  *log.Logger
	service systemsrv.Service
	hosts   hostservice.Service
}

// NewSystemHandler creates a new HTTP handler for system metrics endpoints.
func NewSystemHandler(logger *log.Logger, service systemsrv.Service, hosts hostservice.Service) *SystemHandler {
	return &SystemHandler{
		logger:  logger,
		service: service,
		hosts:   hosts,
	}
}

func parseHostIDQueryOptional(c *gin.Context) uint {
	hostIDStr := c.DefaultQuery("host_id", "0")
	hostID, err := strconv.ParseUint(hostIDStr, 10, 32)
	if err != nil {
		return 0
	}
	return uint(hostID)
}

// HandleCurrentMetrics returns current system metrics for the dashboard.
//
// @Summary     Current system metrics
// @Description Returns an aggregated snapshot of all live system metrics (CPU, memory, disk, network, Docker). Only available for this server instance; remote cluster hosts return empty fields until ingestion is implemented.
// @Tags        metrics
// @Produce     json
// @Param       host_id  query    integer  false  "Host ID (0 = this server instance)"
// @Success     200      {object} map[string]interface{}
// @Failure     401      {object} map[string]string
// @Failure     500      {object} map[string]string
// @Security    BearerAuth
// @Router      /metrics/current [get]
func (h *SystemHandler) HandleCurrentMetrics(c *gin.Context) {
	ctx := c.Request.Context()
	queryHost := parseHostIDQueryOptional(c)

	effective, err := metricshost.EffectiveHostID(ctx, h.hosts, queryHost)
	if errors.Is(err, metricshost.ErrHostNotFound) {
		c.JSON(http.StatusOK, metricshost.EmptyCurrentMetricsPayload())
		return
	}
	if err != nil {
		h.logger.Error("Failed to resolve host for current metrics", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	remote, err := metricshost.IsRemoteHost(ctx, h.hosts, effective)
	if err != nil {
		h.logger.Error("Failed to classify host for current metrics", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if remote {
		c.JSON(http.StatusOK, metricshost.EmptyCurrentMetricsPayload())
		return
	}

	h.logger.Debug("Handling current metrics JSON request", "client_ip", c.ClientIP())
	metrics, err := h.service.CollectAllCurrent(ctx)
	if err != nil {
		h.logger.Error("Failed to get current metrics", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	h.logger.Debug("Current metrics response sent successfully")
	c.JSON(http.StatusOK, metrics)
}
