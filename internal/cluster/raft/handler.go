package raft

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"time"

	"github.com/charmbracelet/log"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// BridgePickerSnapshot is the small interface the handler needs from the
// bridge package; defined here to avoid an import cycle.
type BridgePickerSnapshot interface {
	Snapshot() []any
}

// Handler exposes admin-facing Raft endpoints. It is mounted by the server
// under /api/v1/raft and is safe to register even when the Service is the
// DisabledService — Status() / Ping() still return useful payloads.
type Handler struct {
	svc        Service
	replicator *Replicator
	db         *gorm.DB
	logger     *log.Logger
	clusterID  string
	pickerInfo func() any
	bridgeCfg  BridgeConfigurator
	bootError    func() string
	resetCfg     func() error
	wipeState    func() error
	factoryReset func() error
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

// WithPickerInfo wires a snapshot accessor from the cross-cluster URL picker.
// The handler embeds the snapshot under "bridge_samples" in /raft/status.
func (h *Handler) WithPickerInfo(fn func() any) *Handler {
	h.pickerInfo = fn
	return h
}

// BridgeConfigurator is the small interface the admin "Bridge config"
// panel uses to hot-update the cross-cluster bridge at runtime.
type BridgeConfigurator interface {
	// SaveBridge updates the shared HMAC secret + remote seed list and
	// rebuilds the sender/picker/receiver. AdvertiseURL is optional;
	// when empty the previously configured advertise URL is kept.
	SaveBridge(secret string, remoteSeeds []string, advertiseURL string) error
}

// WithBridgeConfigurator wires the runtime bridge configurator so the
// admin endpoint can apply changes live.
func (h *Handler) WithBridgeConfigurator(b BridgeConfigurator) *Handler {
	h.bridgeCfg = b
	return h
}

// WithBootError wires a getter for the most recent boot-time activation
// failure so /raft/status can surface it to the admin UI.
func (h *Handler) WithBootError(fn func() string) *Handler {
	h.bootError = fn
	return h
}

// WithResetConfig wires a function that wipes RAFT_* from .env so the
// next restart boots Raft-disabled. Used by the admin "Reset" action
// when a bad config is keeping the layer from coming up.
func (h *Handler) WithResetConfig(fn func() error) *Handler {
	h.resetCfg = fn
	return h
}

// WithWipeState wires a function that wipes the Raft log + snapshots
// on disk and re-activates the layer as a fresh single-voter cluster.
// Used to recover from a wedged cluster (e.g. unreachable voter
// preventing quorum) without losing replicated SQLite data.
func (h *Handler) WithWipeState(fn func() error) *Handler {
	h.wipeState = fn
	return h
}

// WithFactoryReset wires a function that fully decouples this node
// from any Raft cluster — wipes data/raft, removes RAFT_* from .env,
// and shuts the running node down. SQLite data is preserved. The
// process comes up Raft-disabled on the next restart.
func (h *Handler) WithFactoryReset(fn func() error) *Handler {
	h.factoryReset = fn
	return h
}

// Status returns the local Raft view.
// GET /api/v1/raft/status
func (h *Handler) Status(c *gin.Context) {
	st := h.svc.Status()
	resp := gin.H{"status": st}
	if h.pickerInfo != nil {
		resp["bridge_samples"] = h.pickerInfo()
	}
	if h.bootError != nil {
		if be := h.bootError(); be != "" {
			resp["boot_error"] = be
		}
	}
	c.JSON(http.StatusOK, resp)
}

// FactoryReset fully decouples this node from any Raft cluster. Wipes
// data/raft on disk + every RAFT_* entry from .env so the next restart
// boots with Raft disabled. The running node is shut down. SQLite tables
// (users, hosts, metrics) are kept.
//
// Use case: the cluster is wedged on multiple nodes and the operator
// wants to start from scratch — call this on every node, then re-run
// the setup wizard.
//
// Admin-only.
//
// POST /api/v1/raft/factory-reset
func (h *Handler) FactoryReset(c *gin.Context) {
	if h.factoryReset == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "factory-reset not wired"})
		return
	}
	if err := h.factoryReset(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"reset": true,
		"next":  "Raft is now disabled on this node. Restart the process to confirm, then re-run the setup wizard.",
	})
}

// ProbeVoterRequest is the body of POST /api/v1/raft/probe-voter.
type ProbeVoterRequest struct {
	Addr string `json:"addr" binding:"required"`
}

// ProbeVoter TCP-dials the given Raft voter address from THIS server,
// reports whether the dial succeeds. Used by the admin "Voters" panel
// to diagnose a wedged cluster — if a voter's advertise address isn't
// reachable from the leader, the cluster can't make progress.
//
// Admin-only.
//
// POST /api/v1/raft/probe-voter
func (h *Handler) ProbeVoter(c *gin.Context) {
	var req ProbeVoterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	addr := req.Addr
	dialer := &net.Dialer{Timeout: 3 * time.Second}
	conn, err := dialer.DialContext(c.Request.Context(), "tcp", addr)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"reachable": false, "addr": addr, "error": err.Error()})
		return
	}
	_ = conn.Close()
	c.JSON(http.StatusOK, gin.H{"reachable": true, "addr": addr})
}

// WipeState shuts the Raft node down, deletes its BoltDB log + snapshot
// files and re-activates as a fresh single-voter bootstrap. Used to
// recover from a wedged cluster (e.g. a voter was added with an
// unreachable advertise address and the cluster can't reach quorum).
// SQLite data — users, hosts, metrics — is preserved.
//
// Admin-only. Dangerous: this throws away the cluster's consensus
// history, so all peers must re-join from scratch.
//
// POST /api/v1/raft/wipe-state
func (h *Handler) WipeState(c *gin.Context) {
	if h.wipeState == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "wipe-state not wired"})
		return
	}
	if err := h.wipeState(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"wiped": true, "next": "this node is now a fresh single-voter cluster; existing peers must re-join"})
}

// ResetConfig wipes RAFT_* settings from .env so the next restart comes
// up Raft-disabled. Useful when a stale config keeps the layer from
// activating at boot (e.g. wrong bind port hardcoded into .env).
// Admin-only.
//
// POST /api/v1/raft/reset
func (h *Handler) ResetConfig(c *gin.Context) {
	if h.resetCfg == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "reset not wired"})
		return
	}
	if err := h.resetCfg(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"reset": true, "next": "restart the process to apply"})
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

// SaveBridgeConfigRequest is the body of POST /api/v1/raft/bridge.
type SaveBridgeConfigRequest struct {
	SharedSecret string   `json:"shared_secret"`
	RemoteSeeds  []string `json:"remote_seeds"`
	AdvertiseURL string   `json:"advertise_url"`
}

// SaveBridgeConfig hot-updates the cross-cluster bridge configuration
// (shared HMAC secret + remote seed URLs + this node's advertise URL).
// Admin-only. The sender / picker / receiver are rebuilt with the new
// values without restarting the process; .env is updated separately by
// the frontend so the config persists across restarts.
//
// POST /api/v1/raft/bridge
func (h *Handler) SaveBridgeConfig(c *gin.Context) {
	if h.svc == nil || !h.svc.Enabled() {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "raft disabled"})
		return
	}
	if h.bridgeCfg == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "bridge configurator not wired"})
		return
	}
	var req SaveBridgeConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := h.bridgeCfg.SaveBridge(req.SharedSecret, req.RemoteSeeds, req.AdvertiseURL); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"saved": true})
}
