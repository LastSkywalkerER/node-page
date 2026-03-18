package handlers

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/charmbracelet/log"
	"github.com/gin-gonic/gin"

	dockerservice "system-stats/internal/modules/docker/application"
	dockerrepos "system-stats/internal/modules/docker/domain/repositories"
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

// DockerHandler handles HTTP requests for Docker container metrics.
type DockerHandler struct {
	logger  *log.Logger
	service dockerservice.Service
}

// NewDockerHandler creates a new HTTP handler for Docker metrics endpoints.
func NewDockerHandler(logger *log.Logger, service dockerservice.Service) *DockerHandler {
	return &DockerHandler{
		logger:  logger,
		service: service,
	}
}

// HandleDockerStats returns Docker container statistics and status information with latest and historical data.
//
// @Summary     Docker metrics
// @Description Returns Docker container stats (running count, resource usage) with history.
// @Tags        metrics
// @Produce     json
// @Param       hours    query    number   false  "History window in hours"  default(0.0833)
// @Param       host_id  query    integer  false  "Host ID (0 = all hosts)"
// @Success     200      {object} map[string]interface{}
// @Failure     401      {object} map[string]string
// @Failure     500      {object} map[string]string
// @Security    BearerAuth
// @Router      /docker [get]
func (h *DockerHandler) HandleDockerStats(c *gin.Context) {
	hours := parseHoursQuery(c)
	hostId := parseHostIdQuery(c)

	ctx := c.Request.Context()
	latestMetrics, err := h.service.GetLatest(ctx)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			h.logger.Info("Client canceled request while fetching latest Docker metrics")
			return
		}
		h.logger.Error("Failed to fetch latest Docker metrics", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":            err.Error(),
			"docker_available": false,
		})
		return
	}

	var historyMetrics []dockerrepos.HistoricalDockerMetric
	if hostId > 0 {
		historyMetrics, err = h.service.GetHistoricalByHost(ctx, hostId, hours)
	} else {
		historyMetrics, err = h.service.GetHistorical(ctx, hours)
	}
	if err != nil {
		if errors.Is(err, context.Canceled) {
			h.logger.Info("Client canceled request while fetching historical Docker metrics")
			return
		}
		h.logger.Error("Failed to fetch historical Docker metrics", "error", err, "hours", hours, "host_id", hostId)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":            err.Error(),
			"docker_available": false,
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"latest":           latestMetrics,
		"history":          historyMetrics,
		"docker_available": true,
	})
}
