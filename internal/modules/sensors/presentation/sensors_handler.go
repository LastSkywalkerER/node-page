package handlers

import (
	"net/http"
	"strconv"

	"github.com/charmbracelet/log"
	"github.com/gin-gonic/gin"

	sensorssrv "system-stats/internal/modules/sensors/application"
)

type SensorsHandler struct {
	logger  *log.Logger
	service sensorssrv.Service
}

func NewSensorsHandler(logger *log.Logger, service sensorssrv.Service) *SensorsHandler {
	return &SensorsHandler{logger: logger, service: service}
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
// @Description Returns temperature sensor data. Returns an empty array on non-Linux hosts.
// @Tags        metrics
// @Produce     json
// @Param       host_id  query    integer  false  "Host ID (ignored — sensors are local-only)"
// @Success     200      {object} map[string]interface{}
// @Failure     401      {object} map[string]string
// @Failure     500      {object} map[string]string
// @Security    BearerAuth
// @Router      /sensors [get]
func (h *SensorsHandler) HandleSensors(c *gin.Context) {
	hostId := parseHostIdQuery(c)
	h.logger.Debug("Handling sensors request", "host_id", hostId)
	metric, err := h.service.Collect(c.Request.Context())
	if err != nil {
		h.logger.Error("Failed to collect sensors", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"sensors": metric})
}
