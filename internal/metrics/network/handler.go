package network

import (
	"context"
	"errors"
	"net/http"

	"github.com/charmbracelet/log"
	"github.com/gin-gonic/gin"

	"system-stats/internal/app/httputil"
	"system-stats/internal/app/metricshost"
	hosts "system-stats/internal/cluster/hosts"
)

type Handler struct {
	logger  *log.Logger
	service Service
	hosts   hosts.Service
}

func NewHandler(logger *log.Logger, service Service, hostsvc hosts.Service) *Handler {
	return &Handler{
		logger:  logger,
		service: service,
		hosts:   hostsvc,
	}
}

func (h *Handler) HandleNetworkStats(c *gin.Context) {
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
