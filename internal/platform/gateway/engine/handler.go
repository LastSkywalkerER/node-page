package engine

import (
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

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
		Host    string `json:"host"`
		HostMAC string `json:"host_mac"`
		Port    int    `json:"port"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": "validation_error", "error": "host and port are required"})
		return
	}
	res, err := h.service.CheckTarget(c.Request.Context(), body.Host, body.HostMAC, body.Port)
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, res)
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

// HandleCheckPublic probes the gateway ports from the internet via check-host.net.
//
// @Summary  Check gateway ports from the internet
// @Tags     gateway
// @Produce  json
// @Success  200 {object} PublicCheckResult
// @Security BearerAuth
// @Router   /gateway/check-public [post]
func (h *Handler) HandleCheckPublic(c *gin.Context) {
	var body struct {
		Target string `json:"target"`
	}
	_ = c.ShouldBindJSON(&body)
	res, err := h.service.CheckPublic(c.Request.Context(), body.Target)
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, res)
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

// forwardedHeader guards against proxy loops between nodes.
const forwardedHeader = "X-NS-Gateway-Forwarded"

// connProxyClient forwards /gateway/connections to the gateway node.
var connProxyClient = &http.Client{
	Timeout:   6 * time.Second,
	Transport: &http.Transport{MaxIdleConns: 2, IdleConnTimeout: 30 * time.Second},
}

// HandleConnections returns the gateway's live connection stats. When this
// node is not the gateway it proxies the call (with the caller's credentials —
// the JWT secret is cluster-shared) to the gateway node's dashboard URL.
//
// @Summary  Gateway connection stats
// @Tags     gateway
// @Produce  json
// @Param    top query integer false "Top clients (default 50)"
// @Param    recent query integer false "Recent requests (default 100)"
// @Success  200 {object} ConnectionsView
// @Security BearerAuth
// @Router   /gateway/connections [get]
func (h *Handler) HandleConnections(c *gin.Context) {
	topN, _ := strconv.Atoi(c.DefaultQuery("top", "50"))
	recentN, _ := strconv.Atoi(c.DefaultQuery("recent", "100"))
	v, err := h.service.Connections(c.Request.Context(), topN, recentN)
	if err != nil {
		h.fail(c, err)
		return
	}
	if !v.Available && v.Reason == "not_gateway" && c.GetHeader(forwardedHeader) == "" {
		if url := h.service.GatewayNodeURL(c.Request.Context()); url != "" {
			if h.proxyConnections(c, url) {
				return
			}
			v.Reason = "the gateway node (" + url + ") did not answer — open its dashboard directly"
		} else {
			v.Reason = "stats live on the gateway node — open its dashboard (its URL is not known here yet)"
		}
	}
	c.JSON(http.StatusOK, v)
}

// proxyConnections relays the request to the gateway node; false = fall back.
func (h *Handler) proxyConnections(c *gin.Context, baseURL string) bool {
	url := baseURL + "/api/v1/gateway/connections"
	if q := c.Request.URL.RawQuery; q != "" {
		url += "?" + q
	}
	req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodGet, url, nil)
	if err != nil {
		return false
	}
	req.Header.Set(forwardedHeader, "1")
	if v := c.GetHeader("Authorization"); v != "" {
		req.Header.Set("Authorization", v)
	}
	if v := c.GetHeader("Cookie"); v != "" {
		req.Header.Set("Cookie", v)
	}
	resp, err := connProxyClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false
	}
	c.Status(http.StatusOK)
	c.Header("Content-Type", "application/json")
	_, _ = io.Copy(c.Writer, io.LimitReader(resp.Body, 4<<20))
	return true
}

// HandleListBlocks lists the blocked clients (replicated — served anywhere).
//
// @Summary  Gateway client blocks
// @Tags     gateway
// @Produce  json
// @Success  200 {object} map[string]interface{}
// @Security BearerAuth
// @Router   /gateway/blocks [get]
func (h *Handler) HandleListBlocks(c *gin.Context) {
	rows, err := h.service.ListBlocks(c.Request.Context())
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"blocks": rows})
}

// HandleCreateBlock blocks a client IP/CIDR.
//
// @Summary  Block a client IP/CIDR on the gateway
// @Tags     gateway
// @Accept   json
// @Produce  json
// @Success  201 {object} map[string]interface{}
// @Security BearerAuth
// @Router   /gateway/blocks [post]
func (h *Handler) HandleCreateBlock(c *gin.Context) {
	var req BlockRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": "validation_error", "error": "invalid block body"})
		return
	}
	req.AdminIP = c.ClientIP()
	req.AdminEmail = c.GetString("userEmail")
	b, err := h.service.CreateBlock(c.Request.Context(), req)
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"block": b})
}

// HandleDeleteBlock unblocks a client.
//
// @Summary  Remove a gateway client block
// @Tags     gateway
// @Produce  json
// @Param    block_id path string true "gateway.Block ID"
// @Success  200 {object} map[string]interface{}
// @Security BearerAuth
// @Router   /gateway/blocks/{block_id} [delete]
func (h *Handler) HandleDeleteBlock(c *gin.Context) {
	if err := h.service.DeleteBlock(c.Request.Context(), c.Param("block_id")); err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "deleted"})
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
