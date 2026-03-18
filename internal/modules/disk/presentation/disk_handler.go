package handlers

import (
	"net/http"
	"strconv"

	"github.com/charmbracelet/log"
	"github.com/gin-gonic/gin"

	diskservice "system-stats/internal/modules/disk/application"
	diskentities "system-stats/internal/modules/disk/infrastructure/entities"
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

// DiskHandler handles HTTP requests for disk metrics.
type DiskHandler struct {
	logger  *log.Logger
	service diskservice.Service
}

// NewDiskHandler creates a new HTTP handler for disk metrics endpoints.
func NewDiskHandler(logger *log.Logger, service diskservice.Service) *DiskHandler {
	return &DiskHandler{
		logger:  logger,
		service: service,
	}
}

// HandleDiskStats returns current disk metrics with latest and historical data.
//
// @Summary     Disk metrics
// @Description Returns latest disk usage snapshot and historical data.
// @Tags        metrics
// @Produce     json
// @Param       hours    query    number   false  "History window in hours"  default(0.0833)
// @Param       host_id  query    integer  false  "Host ID (0 = all hosts)"
// @Success     200      {object} map[string]interface{}
// @Failure     401      {object} map[string]string
// @Failure     500      {object} map[string]string
// @Security    BearerAuth
// @Router      /disk [get]
func (h *DiskHandler) HandleDiskStats(c *gin.Context) {
	hours := parseHoursQuery(c)
	hostId := parseHostIdQuery(c)

	latestMetrics, err := h.service.GetLatest(c.Request.Context())
	if err != nil {
		h.logger.Error("Failed to fetch latest disk metrics", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	var historyMetrics []diskentities.HistoricalDiskMetric
	if hostId > 0 {
		historyMetrics, err = h.service.GetHistoricalByHost(c.Request.Context(), hostId, hours)
	} else {
		historyMetrics, err = h.service.GetHistorical(c.Request.Context(), hours)
	}
	if err != nil {
		h.logger.Error("Failed to fetch historical disk metrics", "error", err, "hours", hours, "host_id", hostId)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"latest":  latestMetrics,
		"history": historyMetrics,
	})
}
