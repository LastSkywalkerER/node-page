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

func (h *SensorsHandler) HandleSensors(c *gin.Context) {
	hostId := parseHostIdQuery(c)
	h.logger.Info("Handling sensors request", "host_id", hostId)
	metric, err := h.service.Collect(c.Request.Context())
	if err != nil {
		h.logger.Error("Failed to collect sensors", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"sensors": metric})
}
