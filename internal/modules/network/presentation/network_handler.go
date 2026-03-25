package handlers

import (
	"context"
	"errors"
	"net/http"

	"github.com/charmbracelet/log"
	"github.com/gin-gonic/gin"

	"system-stats/internal/app/httputil"
	"system-stats/internal/app/metricshost"
	hostservice "system-stats/internal/modules/hosts/application"
	networkservice "system-stats/internal/modules/network/application"
)

// NetworkHandler handles HTTP requests for network metrics.
type NetworkHandler struct {
	logger  *log.Logger
	service networkservice.Service
	hosts   hostservice.Service
}

// NewNetworkHandler creates a new HTTP handler for network metrics endpoints.
func NewNetworkHandler(logger *log.Logger, service networkservice.Service, hosts hostservice.Service) *NetworkHandler {
	return &NetworkHandler{
		logger:  logger,
		service: service,
		hosts:   hosts,
	}
}

// HandleNetworkStats returns current network metrics with latest and historical data.
//
// @Summary     Network metrics
// @Description Returns latest network interface stats and historical traffic data.
// @Tags        metrics
// @Produce     json
// @Param       hours    query    number   false  "History window in hours"  default(0.0833)
// @Param       host_id  query    integer  false  "Host ID (0 = this server instance)"
// @Success     200      {object} map[string]interface{}
// @Failure     401      {object} map[string]string
// @Failure     500      {object} map[string]string
// @Security    BearerAuth
// @Router      /network [get]
func (h *NetworkHandler) HandleNetworkStats(c *gin.Context) {
	hours := httputil.ParseHoursQuery(c)
	queryHost := httputil.ParseHostIdQuery(c)
	ctx := c.Request.Context()

	effective, err := metricshost.EffectiveHostID(ctx, h.hosts, queryHost)
	if errors.Is(err, metricshost.ErrHostNotFound) {
		c.JSON(http.StatusOK, metricshost.EmptyNetworkPayload())
		return
	}
	if err != nil {
		h.logger.Error("Failed to resolve host for network metrics", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	latestMetrics, err := h.service.GetLatestByHost(ctx, effective)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			h.logger.Info("Client canceled request while fetching latest network metrics")
			return
		}
		h.logger.Error("Failed to fetch latest network metrics", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	historyMetrics, err := h.service.GetHistoricalByHost(ctx, effective, hours)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			h.logger.Info("Client canceled request while fetching historical network metrics")
			return
		}
		h.logger.Error("Failed to fetch historical network metrics", "error", err, "hours", hours, "host_id", effective)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"latest":  latestMetrics,
		"history": historyMetrics,
	})
}
