package connectors

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/charmbracelet/log"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// Handler exposes the admin connector endpoints.
type Handler struct {
	logger  *log.Logger
	service Service
}

// NewHandler creates the connectors HTTP handler.
func NewHandler(logger *log.Logger, service Service) *Handler {
	return &Handler{logger: logger, service: service}
}

// HandleList returns discovered hints + configured connectors.
//
// @Summary     List connectors
// @Description Returns environment-detection hints (e.g. "running inside Proxmox") and configured connectors. Admin only.
// @Tags        connectors
// @Produce     json
// @Success     200 {object} map[string]interface{}
// @Security    BearerAuth
// @Router      /connectors [get]
func (h *Handler) HandleList(c *gin.Context) {
	res, err := h.service.List(c.Request.Context())
	if err != nil {
		h.logger.Error("connectors: list failed", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, res)
}

// HandleTestProxmox validates Proxmox credentials without persisting.
//
// @Summary     Test a Proxmox connection
// @Description Validates endpoint + API token and returns a preview (nodes, guest count, matched hosts). Nothing is persisted. Admin only.
// @Tags        connectors
// @Accept      json
// @Produce     json
// @Success     200 {object} map[string]interface{}
// @Failure     400 {object} map[string]string
// @Security    BearerAuth
// @Router      /connectors/proxmox/test [post]
func (h *Handler) HandleTestProxmox(c *gin.Context) {
	var req ConnectRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": "validation_error", "error": "endpoint, token_id and secret are required"})
		return
	}
	preview, err := h.service.TestProxmox(c.Request.Context(), req)
	if err != nil {
		h.respondProbeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"preview": preview})
}

// HandleCreateProxmox validates and persists a Proxmox connector.
//
// @Summary     Connect Proxmox
// @Description Validates the credentials, encrypts the secret and saves the connector (replicated cluster-wide when Raft is on). Re-connecting the same Proxmox (same fingerprint) updates credentials in place. Admin only.
// @Tags        connectors
// @Accept      json
// @Produce     json
// @Success     200 {object} map[string]interface{}
// @Failure     400 {object} map[string]string
// @Security    BearerAuth
// @Router      /connectors [post]
func (h *Handler) HandleCreateProxmox(c *gin.Context) {
	var req ConnectRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": "validation_error", "error": "endpoint, token_id and secret are required"})
		return
	}
	conn, err := h.service.CreateProxmox(c.Request.Context(), req)
	if err != nil {
		h.respondProbeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"connector": conn})
}

// HandleUpdate toggles a connector on/off.
//
// @Summary     Enable/disable a connector
// @Tags        connectors
// @Accept      json
// @Produce     json
// @Param       id path integer true "Connector ID"
// @Success     200 {object} map[string]interface{}
// @Security    BearerAuth
// @Router      /connectors/{id} [patch]
func (h *Handler) HandleUpdate(c *gin.Context) {
	id, ok := h.connectorID(c)
	if !ok {
		return
	}
	var body struct {
		Enabled *bool `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || body.Enabled == nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": "validation_error", "error": "enabled is required"})
		return
	}
	conn, err := h.service.SetEnabled(c.Request.Context(), id, *body.Enabled)
	if err != nil {
		h.respondRepoError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"connector": conn})
}

// HandleDelete removes a connector. ?remove_hosts=true also deletes the
// connector-only host rows it created (agent hosts are only unlinked).
//
// @Summary     Remove a connector
// @Tags        connectors
// @Produce     json
// @Param       id path integer true "Connector ID"
// @Param       remove_hosts query boolean false "Also delete connector-only host rows"
// @Success     200 {object} map[string]interface{}
// @Security    BearerAuth
// @Router      /connectors/{id} [delete]
func (h *Handler) HandleDelete(c *gin.Context) {
	id, ok := h.connectorID(c)
	if !ok {
		return
	}
	removeHosts := c.Query("remove_hosts") == "true"
	if err := h.service.Delete(c.Request.Context(), id, removeHosts); err != nil {
		h.respondRepoError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"removed": id})
}

// HandleSync requests an immediate poller resync.
//
// @Summary     Force a connector sync
// @Tags        connectors
// @Produce     json
// @Success     202 {object} map[string]interface{}
// @Security    BearerAuth
// @Router      /connectors/{id}/sync [post]
func (h *Handler) HandleSync(c *gin.Context) {
	h.service.TriggerSync()
	c.JSON(http.StatusAccepted, gin.H{"status": "sync_requested"})
}

func (h *Handler) connectorID(c *gin.Context) (uint, bool) {
	id64, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id64 == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid connector id"})
		return 0, false
	}
	return uint(id64), true
}

func (h *Handler) respondProbeError(c *gin.Context, err error) {
	if errors.Is(err, ErrAuthFailed) {
		c.JSON(http.StatusBadRequest, gin.H{"code": "auth_failed", "error": err.Error()})
		return
	}
	h.logger.Warn("connectors: probe failed", "error", err)
	c.JSON(http.StatusBadGateway, gin.H{"code": "unreachable", "error": err.Error()})
}

func (h *Handler) respondRepoError(c *gin.Context, err error) {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "connector not found"})
		return
	}
	h.logger.Error("connectors: request failed", "error", err)
	c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
}
