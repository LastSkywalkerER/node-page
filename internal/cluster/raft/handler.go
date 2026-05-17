package raft

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// Handler exposes admin-facing Raft endpoints. It is mounted by the server
// under /api/v1/raft and is safe to register even when the Service is the
// DisabledService — Status() still returns a useful "disabled" payload.
type Handler struct {
	svc Service
}

// NewHandler wires the Service.
func NewHandler(svc Service) *Handler { return &Handler{svc: svc} }

// Status returns the local Raft view.
// GET /api/v1/raft/status
func (h *Handler) Status(c *gin.Context) {
	c.JSON(http.StatusOK, h.svc.Status())
}

// Ping is a lightweight liveness probe used by the cross-cluster URL picker
// to measure RTT to peer nodes. Public on purpose (no JWT) so peer clusters
// can probe without sharing user credentials; the bridge endpoints that
// actually mutate state are HMAC-authenticated separately.
//
// GET /api/v1/raft/ping
func (h *Handler) Ping(c *gin.Context) {
	st := h.svc.Status()
	c.Header("X-Raft-Cluster-ID", st.ClusterID)
	c.Header("X-Raft-Node-ID", st.NodeID)
	c.Header("X-Raft-State", st.State)
	c.Status(http.StatusNoContent)
}
