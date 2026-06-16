package pbs

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

// Handler serves the PBS datastore/backup detail for the backup-server widget.
type Handler struct{ poller *Poller }

// NewHandler creates the PBS detail handler.
func NewHandler(poller *Poller) *Handler { return &Handler{poller: poller} }

// HandleGet returns the datastore/backup detail for a PBS host_id.
//
// @Summary     PBS datastore + backup detail
// @Description Returns datastore capacity and backup health for a Proxmox Backup Server host. The snapshot is maintained by the polling node (leader); an empty payload means this node isn't currently polling that host.
// @Tags        pbs
// @Produce     json
// @Param       host_id query integer true "PBS host id"
// @Success     200 {object} map[string]interface{}
// @Security    BearerAuth
// @Router      /pbs [get]
func (h *Handler) HandleGet(c *gin.Context) {
	id64, err := strconv.ParseUint(c.Query("host_id"), 10, 64)
	if err != nil || id64 == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "host_id is required"})
		return
	}
	if s, ok := h.poller.Snapshot(uint(id64)); ok {
		c.JSON(http.StatusOK, s)
		return
	}
	// Not polled here (or no detail yet) — empty but well-formed.
	c.JSON(http.StatusOK, Status{HostID: uint(id64), Datastores: []Datastore{}})
}
