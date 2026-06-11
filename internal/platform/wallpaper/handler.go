package wallpaper

import (
	"net/http"

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
		c.Status(http.StatusNoContent)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": w})
}
