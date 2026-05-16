package disk

import (
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

func (h *Handler) HandleDiskStats(c *gin.Context) {
	hours := httputil.ParseHoursQuery(c)
	queryHost := httputil.ParseHostIdQuery(c)

	effective, err := metricshost.EffectiveHostID(c.Request.Context(), h.hosts, queryHost)
	if errors.Is(err, metricshost.ErrHostNotFound) {
		c.JSON(http.StatusOK, metricshost.EmptyDiskPayload())
		return
	}
	if err != nil {
		h.logger.Error("Failed to resolve host for disk metrics", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	latestMetrics, err := h.service.GetLatestByHost(c.Request.Context(), effective)
	if err != nil {
		h.logger.Error("Failed to fetch latest disk metrics", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	historyMetrics, err := h.service.GetHistoricalByHost(c.Request.Context(), effective, hours)
	if err != nil {
		h.logger.Error("Failed to fetch historical disk metrics", "error", err, "hours", hours, "host_id", effective)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"latest":  latestMetrics,
		"history": historyMetrics,
	})
}
