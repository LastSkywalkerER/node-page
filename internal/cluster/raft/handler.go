package raft

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"time"

	"github.com/charmbracelet/log"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// Handler exposes admin-facing Raft endpoints. It is mounted by the server
// under /api/v1/raft and is safe to register even when the Service is the
// DisabledService — Status() / Ping() still return useful payloads.
type Handler struct {
	svc        Service
	replicator *Replicator
	db         *gorm.DB
	logger     *log.Logger
	clusterID  string
}

// NewHandler wires the Service. The Replicator and DB are required for the
// peer-management endpoints (POST /raft/peers, POST /raft/join-token,
// POST /raft/join); pass nil when Raft is disabled and only the read-only
// Status / Ping endpoints will be registered.
func NewHandler(svc Service) *Handler { return &Handler{svc: svc} }

// WithDeps wires the dependencies needed for the mutating endpoints.
func (h *Handler) WithDeps(replicator *Replicator, db *gorm.DB, logger *log.Logger, clusterID string) *Handler {
	h.replicator = replicator
	h.db = db
	h.logger = logger
	h.clusterID = clusterID
	return h
}

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

// AddPeer adds a Raft voter to the current cluster. Leader-only.
// POST /api/v1/raft/peers   { id, addr }
func (h *Handler) AddPeer(c *gin.Context) {
	if h.svc == nil || !h.svc.Enabled() {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "raft disabled"})
		return
	}
	var req struct {
		ID   string `json:"id"   binding:"required"`
		Addr string `json:"addr" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := h.svc.AddVoter(req.ID, req.Addr); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"added": req.ID})
}

// RemovePeer removes a Raft server by id. Leader-only.
// DELETE /api/v1/raft/peers/:id
func (h *Handler) RemovePeer(c *gin.Context) {
	if h.svc == nil || !h.svc.Enabled() {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "raft disabled"})
		return
	}
	id := c.Param("id")
	if id == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing id"})
		return
	}
	if err := h.svc.RemovePeer(id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"removed": id})
}

// IssueJoinToken generates a one-shot bootstrap token and replicates its
// hash via CmdJoinTokenIssue. The plaintext token is returned to the caller
// exactly once and never persisted. Admin-only.
//
// POST /api/v1/raft/join-token   { ttl_minutes? }
func (h *Handler) IssueJoinToken(c *gin.Context) {
	if h.svc == nil || !h.svc.Enabled() || h.replicator == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "raft disabled"})
		return
	}
	var req struct {
		TTLMinutes int `json:"ttl_minutes"`
	}
	_ = c.ShouldBindJSON(&req)
	if req.TTLMinutes <= 0 {
		req.TTLMinutes = 60
	}

	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "rand: " + err.Error()})
		return
	}
	plaintext := hex.EncodeToString(raw)
	sum := sha256.Sum256([]byte(plaintext))
	tokenHash := hex.EncodeToString(sum[:])
	expiresAt := time.Now().UTC().Add(time.Duration(req.TTLMinutes) * time.Minute)

	var createdBy uint
	if uid, ok := c.Get("userID"); ok {
		if u, ok := uid.(uint); ok {
			createdBy = u
		}
	}

	if err := h.replicator.SubmitJoinTokenIssue(c.Request.Context(), tokenHash, expiresAt, createdBy); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"token":      plaintext,
		"expires_at": expiresAt,
		"cluster_id": h.clusterID,
	})
}

// JoinRequest is the body of POST /api/v1/raft/join.
type JoinRequest struct {
	Token         string `json:"token"          binding:"required"`
	NodeID        string `json:"node_id"        binding:"required"`
	AdvertiseAddr string `json:"advertise_addr" binding:"required"`
}

// Join is the public, token-authenticated endpoint a fresh node calls to
// enter the cluster. The server verifies the token (must exist, not be
// consumed, not be expired), marks it consumed, adds the caller as a Raft
// voter and returns the current cluster id + the peer-list snapshot so the
// joiner can configure its local node.
//
// POST /api/v1/raft/join
func (h *Handler) Join(c *gin.Context) {
	if h.svc == nil || !h.svc.Enabled() || h.replicator == nil || h.db == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "raft disabled"})
		return
	}
	if !h.svc.IsLeader() {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "not the leader; retry against the cluster leader"})
		return
	}
	var req JoinRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	sum := sha256.Sum256([]byte(req.Token))
	tokenHash := hex.EncodeToString(sum[:])

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	tok, err := LookupJoinToken(ctx, h.db, tokenHash)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if tok == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unknown join token"})
		return
	}
	if tok.ConsumedAt != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "token already used"})
		return
	}
	if time.Now().UTC().After(tok.ExpiresAt) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "token expired"})
		return
	}

	// Add as voter first; if that fails we don't consume the token.
	if err := h.svc.AddVoter(req.NodeID, req.AdvertiseAddr); err != nil {
		if errors.Is(err, ErrDisabled) {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if err := h.replicator.SubmitJoinTokenConsume(ctx, tokenHash, req.NodeID, req.AdvertiseAddr); err != nil {
		// Token-consume failure is best-effort; the AddVoter already
		// succeeded so the new node can join. Log via the standard
		// logger; the caller still gets a 200.
		if h.logger != nil {
			h.logger.Warn("join: SubmitJoinTokenConsume failed", "error", err)
		}
	}
	st := h.svc.Status()
	c.JSON(http.StatusOK, gin.H{
		"cluster_id":   st.ClusterID,
		"leader_id":    st.LeaderID,
		"leader_addr":  st.LeaderAddr,
		"peers":        st.Peers,
		"applied_idx":  st.AppliedIndex,
	})
}
