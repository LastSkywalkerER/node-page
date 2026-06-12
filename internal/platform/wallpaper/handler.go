package wallpaper

import (
	"fmt"
	"net/http"
	"time"

	"github.com/charmbracelet/log"
	"github.com/gin-gonic/gin"
)

// Handler serves the wallpaper proxy endpoint.
type Handler struct {
	logger  *log.Logger
	service *Service
}

// NewHandler creates the wallpaper HTTP handler.
func NewHandler(logger *log.Logger, service *Service) *Handler {
	return &Handler{logger: logger, service: service}
}

// HandleCurrent returns the wallpaper for the requested UI mode.
//
// @Summary     Current dynamic wallpaper
// @Description Proxies the configured Pexels connector: returns the photo every client should show right now (rotates every 5 minutes), or 204 when no wallpaper connector is configured.
// @Tags        wallpaper
// @Produce     json
// @Param       mode query string false "UI mode: dark (default) or light"
// @Success     200 {object} map[string]interface{}
// @Success     204 {string} string "no wallpaper connector configured"
// @Security    BearerAuth
// @Router      /wallpaper [get]
func (h *Handler) HandleCurrent(c *gin.Context) {
	w, err := h.service.Current(c.Request.Context(), c.DefaultQuery("mode", "dark"))
	if err != nil {
		h.logger.Warn("wallpaper: request failed", "error", err)
		c.JSON(http.StatusBadGateway, gin.H{"code": "wallpaper_unavailable", "error": err.Error()})
		return
	}
	if w == nil {
		// Re-check occasionally: the operator may connect Pexels any minute.
		c.Header("Cache-Control", "private, max-age=60")
		c.Status(http.StatusNoContent)
		return
	}
	// The rotation bucket is wall-clock aligned (unix/300): every client may
	// cache the answer until the next 5-minute boundary — repeated mounts,
	// extra tabs and the periodic refetch all hit the browser cache instead
	// of this endpoint (and the image itself comes from the Pexels CDN, the
	// API key is only spent on the hourly server-side search).
	untilNextBucket := 300 - time.Now().Unix()%300
	c.Header("Cache-Control", fmt.Sprintf("private, max-age=%d", untilNextBucket))
	c.JSON(http.StatusOK, gin.H{"data": w})
}
