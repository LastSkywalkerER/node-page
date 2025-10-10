package handlers

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/charmbracelet/log"
	"github.com/gin-gonic/gin"

	networkservice "system-stats/internal/modules/network/application"
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
func (h *NetworkHandler) HandleNetworkStats(c *gin.Context) {
	h.logger.Info("Handling network stats request", "client_ip", c.ClientIP(), "user_agent", c.GetHeader("User-Agent"))

	hours := parseHoursQuery(c)
	hostId := parseHostIdQuery(c)

	// Get latest network metrics from database
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

	// Get historical network metrics (filtered by host_id if provided)
	var historyMetrics []interface{}
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

	h.logger.Info("Network stats response sent successfully", "interfaces_count", len(latestMetrics.Interfaces), "history_points", len(historyMetrics), "host_id", hostId)
	c.JSON(http.StatusOK, gin.H{
		"latest":  latestMetrics,
		"history": historyMetrics,
	})
}

// HandleNetworkHistory returns network-specific historical metrics for the requested time range.
func (h *NetworkHandler) HandleNetworkHistory(c *gin.Context) {
	hours := parseHoursQuery(c)
	h.logger.Info("Handling network history request", "client_ip", c.ClientIP(), "hours", hours)
	history, handled := h.fetchHistory(c, hours)
	if handled {
		return
	}
	h.logger.Info("Network history response sent successfully", "hours", hours)
	c.JSON(http.StatusOK, gin.H{"network": history})
}

// fetchHistory loads historical metrics and writes an error response if needed.
func (h *NetworkHandler) fetchHistory(c *gin.Context, hours float64) ([]interface{}, bool) {
	h.logger.Info("Fetching network historical metrics", "hours", hours)
	history, err := h.service.GetHistorical(c.Request.Context(), hours)
	if err != nil {
		h.logger.Error("Failed to fetch network historical metrics", "error", err, "hours", hours)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return nil, true
	}
	h.logger.Info("Network historical metrics fetched successfully", "data_points", len(history))
	return history, false
}
