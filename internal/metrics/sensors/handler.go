package sensors

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
	return &Handler{logger: logger, service: service, hosts: hostsvc}
}

func (h *Handler) HandleSensors(c *gin.Context) {
	ctx := c.Request.Context()
	queryHost := httputil.ParseHostIdQuery(c)

	effective, err := metricshost.EffectiveHostID(ctx, h.hosts, queryHost)
	if errors.Is(err, metricshost.ErrHostNotFound) {
		c.JSON(http.StatusOK, metricshost.EmptySensorsPayload())
		return
	}
	if err != nil {
		h.logger.Error("Failed to resolve host for sensors", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	remote, err := metricshost.IsRemoteHost(ctx, h.hosts, effective)
	if err != nil {
		h.logger.Error("Failed to classify host for sensors", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if remote {
		c.JSON(http.StatusOK, metricshost.EmptySensorsPayload())
		return
	}

	h.logger.Debug("Handling sensors request", "host_id", effective)
	metric, err := h.service.Collect(ctx)
	if err != nil {
		h.logger.Error("Failed to collect sensors", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"sensors": metric})
}
