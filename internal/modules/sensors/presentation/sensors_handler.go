package handlers

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/charmbracelet/log"
	"github.com/gin-gonic/gin"

	"system-stats/internal/app/metricshost"
	hostservice "system-stats/internal/modules/hosts/application"
	sensorssrv "system-stats/internal/modules/sensors/application"
)

type SensorsHandler struct {
	logger  *log.Logger
	service sensorssrv.Service
	hosts   hostservice.Service
}

func NewSensorsHandler(logger *log.Logger, service sensorssrv.Service, hosts hostservice.Service) *SensorsHandler {
	return &SensorsHandler{logger: logger, service: service, hosts: hosts}
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

// HandleSensors returns temperature sensor readings.
//
// @Summary     Sensor readings
// @Description Returns temperature sensor data for this server instance only (Linux). Empty for remote hosts or non-Linux.
// @Tags        metrics
// @Produce     json
// @Param       host_id  query    integer  false  "Host ID (0 = this server instance)"
// @Success     200      {object} map[string]interface{}
// @Failure     401      {object} map[string]string
// @Failure     500      {object} map[string]string
// @Security    BearerAuth
// @Router      /sensors [get]
func (h *SensorsHandler) HandleSensors(c *gin.Context) {
	ctx := c.Request.Context()
	queryHost := parseHostIdQuery(c)

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
