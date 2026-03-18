package handlers

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/charmbracelet/log"
	"github.com/gin-gonic/gin"

	networkservice "system-stats/internal/modules/network/application"
	networkentities "system-stats/internal/modules/network/infrastructure/entities"
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

// NetworkHandler handles HTTP requests for network metrics.
type NetworkHandler struct {
	logger  *log.Logger
	service networkservice.Service
}

// NewNetworkHandler creates a new HTTP handler for network metrics endpoints.
func NewNetworkHandler(logger *log.Logger, service networkservice.Service) *NetworkHandler {
	return &NetworkHandler{
		logger:  logger,
		service: service,
	}
}

// HandleNetworkStats returns current network metrics with latest and historical data.
//
// @Summary     Network metrics
// @Description Returns latest network interface stats and historical traffic data.
// @Tags        metrics
// @Produce     json
// @Param       hours    query    number   false  "History window in hours"  default(0.0833)
// @Param       host_id  query    integer  false  "Host ID (0 = all hosts)"
// @Success     200      {object} map[string]interface{}
// @Failure     401      {object} map[string]string
// @Failure     500      {object} map[string]string
// @Security    BearerAuth
// @Router      /network [get]
func (h *NetworkHandler) HandleNetworkStats(c *gin.Context) {
	hours := parseHoursQuery(c)
	hostId := parseHostIdQuery(c)

	ctx := c.Request.Context()
	latestMetrics, err := h.service.GetLatest(ctx)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			h.logger.Info("Client canceled request while fetching latest network metrics")
			return
		}
		h.logger.Error("Failed to fetch latest network metrics", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	var historyMetrics []networkentities.NetworkMetric
	if hostId > 0 {
		historyMetrics, err = h.service.GetHistoricalByHost(ctx, hostId, hours)
	} else {
		historyMetrics, err = h.service.GetHistorical(ctx, hours)
	}
	if err != nil {
		if errors.Is(err, context.Canceled) {
			h.logger.Info("Client canceled request while fetching historical network metrics")
			return
		}
		h.logger.Error("Failed to fetch historical network metrics", "error", err, "hours", hours, "host_id", hostId)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"latest":  latestMetrics,
		"history": historyMetrics,
	})
}
