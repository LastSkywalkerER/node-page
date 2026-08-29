package engine

import (
	"errors"
	"net/http"
	"strconv"
	"system-stats/internal/platform/gateway"

	"github.com/charmbracelet/log"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// Handler exposes the admin gateway endpoints.
type Handler struct {
	logger  *log.Logger
	service Service
}

// NewHandler creates the gateway HTTP handler.
func NewHandler(logger *log.Logger, service Service) *Handler {
	return &Handler{logger: logger, service: service}
}

// HandleGet returns config + routes + this node's status/capabilities.
//
// @Summary  Gateway state
// @Tags     gateway
// @Produce  json
// @Success  200 {object} State
// @Security BearerAuth
// @Router   /gateway [get]
func (h *Handler) HandleGet(c *gin.Context) {
	st, err := h.service.GetState(c.Request.Context())
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, st)
}

// HandleSetConfig replaces the gateway configuration (cluster-wide).
//
// @Summary  Set gateway config
// @Tags     gateway
// @Accept   json
// @Produce  json
// @Success  200 {object} map[string]interface{}
// @Security BearerAuth
// @Router   /gateway/config [put]
func (h *Handler) HandleSetConfig(c *gin.Context) {
	var cfg gateway.Config
	if err := c.ShouldBindJSON(&cfg); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": "validation_error", "error": "invalid config body"})
		return
	}
	out, err := h.service.SetConfig(c.Request.Context(), cfg)
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"config": out})
}

// HandleTargets lists suggested upstreams (published container ports cluster-wide).
//
// @Summary  Gateway target suggestions
// @Tags     gateway
// @Produce  json
// @Success  200 {object} map[string]interface{}
// @Security BearerAuth
// @Router   /gateway/targets [get]
func (h *Handler) HandleTargets(c *gin.Context) {
	t, err := h.service.Targets(c.Request.Context())
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"targets": t})
}

// HandleCheck TCP-dials a target from this node.
//
// @Summary  Check a gateway target
// @Tags     gateway
// @Accept   json
// @Produce  json
// @Success  200 {object} map[string]interface{}
// @Security BearerAuth
// @Router   /gateway/check [post]
func (h *Handler) HandleCheck(c *gin.Context) {
	var body struct {
		Host string `json:"host"`
		Port int    `json:"port"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": "validation_error", "error": "host and port are required"})
		return
	}
	if err := h.service.CheckTarget(c.Request.Context(), body.Host, body.Port); err != nil {
		if errors.Is(err, ErrValidation) {
			h.fail(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"reachable": false, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"reachable": true})
}

// HandleCreateRoute adds a route.
//
// @Summary  Create a gateway route
// @Tags     gateway
// @Accept   json
// @Produce  json
// @Success  201 {object} map[string]interface{}
// @Security BearerAuth
// @Router   /gateway/routes [post]
func (h *Handler) HandleCreateRoute(c *gin.Context) {
	var req RouteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": "validation_error", "error": "invalid route body"})
		return
	}
	v, err := h.service.CreateRoute(c.Request.Context(), req)
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"route": v})
}

// HandleUpdateRoute replaces a route.
//
// @Summary  Update a gateway route
// @Tags     gateway
// @Accept   json
// @Produce  json
// @Param    route_id path string true "gateway.Route ID"
// @Success  200 {object} map[string]interface{}
// @Security BearerAuth
// @Router   /gateway/routes/{route_id} [put]
func (h *Handler) HandleUpdateRoute(c *gin.Context) {
	var req RouteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": "validation_error", "error": "invalid route body"})
		return
	}
	v, err := h.service.UpdateRoute(c.Request.Context(), c.Param("route_id"), req)
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"route": v})
}

// HandleDeleteRoute removes a route.
//
// @Summary  Delete a gateway route
// @Tags     gateway
// @Produce  json
// @Param    route_id path string true "gateway.Route ID"
// @Success  200 {object} map[string]interface{}
// @Security BearerAuth
// @Router   /gateway/routes/{route_id} [delete]
func (h *Handler) HandleDeleteRoute(c *gin.Context) {
	if err := h.service.DeleteRoute(c.Request.Context(), c.Param("route_id")); err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "deleted"})
}

// HandleLogs returns the managed Traefik's recent logs (gateway node only).
//
// @Summary  Gateway (Traefik) logs
// @Tags     gateway
// @Produce  json
// @Param    tail query integer false "Lines (default 300, max 2000)"
// @Success  200 {object} map[string]interface{}
// @Security BearerAuth
// @Router   /gateway/logs [get]
func (h *Handler) HandleLogs(c *gin.Context) {
	tail, _ := strconv.Atoi(c.DefaultQuery("tail", "300"))
	out, err := h.service.Logs(c.Request.Context(), tail)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"logs": out, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"logs": out})
}

func (h *Handler) fail(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrValidation):
		c.JSON(http.StatusBadRequest, gin.H{"code": "validation_error", "error": err.Error()})
	case errors.Is(err, gorm.ErrRecordNotFound):
		c.JSON(http.StatusNotFound, gin.H{"code": "not_found", "error": "route not found"})
	default:
		h.logger.Error("gateway: request failed", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
	}
}
