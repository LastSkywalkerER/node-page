package raft

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
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
	svc          Service
	replicator   *Replicator
	db           *gorm.DB
	logger       *log.Logger
	clusterID    string
	pickerInfo   func() any
	bridgeInfo   func() any
	bridgeCfg    BridgeConfigurator
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

// WithBridgeInfo wires a snapshot accessor for the bridge runtime state
// (mode + sender health). Embedded under "bridge" in /raft/status.
func (h *Handler) WithBridgeInfo(fn func() any) *Handler {
	h.bridgeInfo = fn
	return h
}

// BridgeConfigurator is the small interface the admin "Bridge config"
// panel uses to hot-update the cross-cluster bridge at runtime.
type BridgeConfigurator interface {
	// SaveBridge updates the bridge mode, shared HMAC secret + remote seed list and
	// rebuilds the sender/picker/receiver. AdvertiseURL is optional;
	// when empty the previously configured advertise URL is kept.
	SaveBridge(secret string, remoteSeeds []string, advertiseURL, mode string) error
	// BridgeSecret returns the currently configured shared HMAC secret
	// ('' when the bridge was never configured) so the admin UI can offer
	// it for copying when enrolling additional sites.
	BridgeSecret() string
	// BridgeSettings returns the full saved uplink configuration so the
	// admin form prefills instead of starting blank.
	BridgeSettings() (mode, secret string, seeds []string, advertiseURL string)
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
	if stats := h.svc.Stats(); stats != nil {
		// Pass through the full hashicorp/raft Stats() map so the admin
		// UI can render last_contact / num_peers / latest_configuration
		// for diagnostics when the cluster fails to elect.
		resp["raft_stats"] = stats
	}
	if h.bridgeInfo != nil {
		if bi := h.bridgeInfo(); bi != nil {
			resp["bridge"] = bi
		}
	}
	// Uplinked spoke clusters (hub side): summarized from the bridge dedupe
	// table. Raw table name to avoid a raft→bridge import cycle.
	if h.db != nil {
		var uplinks []struct {
			OriginClusterID string `gorm:"column:origin_cluster_id" json:"cluster_id"`
			LastIndex       uint64 `gorm:"column:last_index" json:"last_origin_index"`
			// String, not time.Time: the aggregated MAX(applied_at) comes back
			// as a raw SQLite string and fails the time scan otherwise.
			LastAt string `gorm:"column:last_at" json:"last_applied_at"`
		}
		err := h.db.WithContext(c.Request.Context()).
			Table("applied_remote_log").
			Select("origin_cluster_id, MAX(origin_index) AS last_index, MAX(applied_at) AS last_at").
			Group("origin_cluster_id").
			Scan(&uplinks).Error
		if err == nil && len(uplinks) > 0 {
			resp["uplinks"] = uplinks
		}
	}
	// Advertised HTTP URLs from the peer catalog: lets the UI attach a
	// clickable address to each cluster node (matched to hosts by the IP in
	// the voter's advertise addr).
	if h.db != nil {
		var rows []peerNodeAdvertise
		if err := h.db.WithContext(c.Request.Context()).Find(&rows).Error; err == nil && len(rows) > 0 {
			urls := make([]gin.H, 0, len(rows))
			for _, r := range rows {
				urls = append(urls, gin.H{"cluster_id": r.ClusterID, "node_id": r.NodeID, "url": r.URL})
			}
			resp["peer_urls"] = urls
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

// Leave removes THIS node from the Raft cluster and decouples it to standalone.
//
// Membership removal MUST happen while this node is still up — otherwise a small
// cluster (e.g. 2 voters) loses quorum the moment this node stops and can never
// commit the removal. The membership change is ALWAYS performed by a *different*
// leader (never self-removal, which is fragile in hashicorp/raft): a leaving
// leader first transfers leadership to a healthy follower, then leaves as a
// follower. Sequence:
//  1. Leader & only voter → nothing to remove; just decouple.
//  2. Leader with peers   → transfer leadership away, then continue as a follower.
//  3. Follower            → ask the current leader to RemoveServer(self) over
//     HTTP, forwarding the admin's credentials (JWT_SECRET is shared
//     cluster-wide, so the leader accepts them).
//  4. factory-reset locally → stop Raft, wipe data/raft, clear RAFT_* from .env.
//
// The node is only decoupled (step 4) once membership removal has been confirmed
// (or it's the last node) — a failed removal returns an error and leaves the node
// in the cluster, so a small cluster can't be wedged by a half-completed leave.
//
// Admin-only.
//
// POST /api/v1/raft/leave
func (h *Handler) Leave(c *gin.Context) {
	if h.factoryReset == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "leave not wired"})
		return
	}
	st := h.svc.Status()
	if !st.Enabled {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Raft is not enabled on this node"})
		return
	}
	self := st.NodeID
	voters := 0
	for _, p := range st.Peers {
		if p.Suffrage == "voter" {
			voters++
		}
	}

	// Last node standing: nothing to remove from membership, just decouple.
	if strings.EqualFold(st.State, "Leader") && voters <= 1 {
		h.finishLeave(c, self)
		return
	}

	// Leader of a multi-voter cluster: step down first so the membership change is
	// made by another leader (self-removal while leading is fragile / can wedge a
	// 2-node cluster). After a successful transfer we are a follower.
	if strings.EqualFold(st.State, "Leader") {
		if err := h.svc.TransferLeadership(); err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "could not hand off leadership before leaving; retry shortly or leave from another node: " + err.Error()})
			return
		}
		st = h.svc.Status() // refresh — should now be a follower with a new leader
	}

	// Follower / candidate: only the (other) leader can change membership. But a
	// node whose JOIN never completed — or a lone node that never formed a quorum
	// — sits here forever with no elected leader. There is no remote membership to
	// remove (this node was never a committed voter in a working cluster), so
	// decouple it locally instead of wedging the operator with a 503. A genuine
	// multi-voter cluster that merely lost quorum keeps the strict path; the
	// operator can still force out via Factory reset.
	if st.LeaderID == "" || strings.EqualFold(st.LeaderID, self) {
		liveVoters := 0
		for _, p := range st.Peers {
			if p.Suffrage == "voter" {
				liveVoters++
			}
		}
		if liveVoters <= 1 {
			h.finishLeave(c, self)
			return
		}
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "no reachable leader to process leave (cluster has no quorum); retry shortly, or use Factory reset to force this node out"})
		return
	}
	if h.db == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "leave not available (no db)"})
		return
	}
	lookupCtx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
	leaderURL, err := LookupPeerURL(lookupCtx, h.db, h.clusterID, st.LeaderID)
	cancel()
	if err != nil || leaderURL == "" {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "leader URL unknown; cannot leave cleanly (retry shortly, or factory-reset)"})
		return
	}
	if err := h.requestLeaderRemove(c, leaderURL, self); err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "leader did not remove this node (it stays in the cluster): " + err.Error()})
		return
	}

	h.finishLeave(c, self)
}

// finishLeave decouples this node to standalone after its membership has been
// removed (or it was the last node).
func (h *Handler) finishLeave(c *gin.Context, self string) {
	if err := h.factoryReset(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "decouple to standalone failed: " + err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"left":    true,
		"node_id": self,
		"next":    "This node left the cluster and is now standalone. Restart to confirm.",
	})
}

// requestLeaderRemove asks the leader to RemoveServer(nodeID) via its admin
// kick endpoint, forwarding the caller's credentials (the cluster shares
// JWT_SECRET so the leader authenticates the same admin token).
func (h *Handler) requestLeaderRemove(c *gin.Context, leaderURL, nodeID string) error {
	url := strings.TrimRight(leaderURL, "/") + "/api/v1/raft/peers/" + nodeID
	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, nil)
	if err != nil {
		return err
	}
	if cookie := c.GetHeader("Cookie"); cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	if authz := c.GetHeader("Authorization"); authz != "" {
		req.Header.Set("Authorization", authz)
	}
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<10))
		return fmt.Errorf("leader %s returned %s: %s", leaderURL, resp.Status, strings.TrimSpace(string(body)))
	}
	return nil
}

// ProbeVoterRequest is the body of POST /api/v1/raft/probe-voter.
type ProbeVoterRequest struct {
	Addr string `json:"addr" binding:"required"`
}

// Forward accepts a Command from a follower and applies it locally if
// this node is the leader. Used by the SubmitCommand leader-forwarding
// path so followers' writes (e.g. their own host info) end up in the
// replicated log even when they originate on a non-leader.
//
// Public endpoint. The Command payload is opaque host/user state that
// only matters within the trusted cluster network; for production
// deployments where the cluster spans untrusted networks, layer HMAC
// (similar to /raft/bridge/replicate) on top — TODO.
//
// POST /api/v1/raft/forward
func (h *Handler) Forward(c *gin.Context) {
	if h.svc == nil || !h.svc.Enabled() {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "raft disabled"})
		return
	}
	if !h.svc.IsLeader() {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "not the leader; retry against the cluster leader"})
		return
	}
	var cmd Command
	if err := c.ShouldBindJSON(&cmd); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	timeout := 5 * time.Second
	res, err := h.svc.SubmitCommand(c.Request.Context(), cmd, timeout)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// SubmitResult.Err is an `error` interface and cannot round-trip through
	// JSON — send the JSON-safe wire form so the forwarding follower can
	// decode it.
	c.JSON(http.StatusOK, res.ToWire())
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
	// HTTPURL is the joiner's externally-reachable HTTP base URL
	// (e.g. http://192.168.0.104:9090). Required for SubmitCommand
	// forwarding so the leader can reply back to this voter when it
	// later sits as a follower trying to submit a write.
	HTTPURL string `json:"http_url"`
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

	// Add as a NON-voter first; if that fails we don't consume the token.
	// A non-voter replicates the full log/snapshot (so the joiner converges
	// and can serve reads) but does not count toward quorum — so a node that
	// joins with a bad/unreachable advertise address can never wedge the
	// cluster's write quorum. The leader's membership manager promotes it to a
	// voter once it has stayed reachable (see membership.go).
	if err := h.svc.AddNonvoter(req.NodeID, req.AdvertiseAddr); err != nil {
		if errors.Is(err, ErrDisabled) {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Publish the joiner's HTTP URL into peer_node_advertise so the
	// future follower→leader SubmitCommand forwarding can resolve it.
	// Best-effort; missing http_url just means the joiner can't forward
	// its own writes (host info etc.) — works once they advertise on
	// their next post-activate hook.
	if req.HTTPURL != "" {
		if perr := h.replicator.SubmitPeerNodeAdvertise(ctx, h.clusterID, req.NodeID, req.HTTPURL, nil); perr != nil && h.logger != nil {
			h.logger.Warn("raft join: failed to publish joiner URL", "node_id", req.NodeID, "error", perr)
		}
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
		"cluster_id":  st.ClusterID,
		"leader_id":   st.LeaderID,
		"leader_addr": st.LeaderAddr,
		"peers":       st.Peers,
		"applied_idx": st.AppliedIndex,
	})
}

// SaveBridgeConfigRequest is the body of POST /api/v1/raft/bridge.
type SaveBridgeConfigRequest struct {
	SharedSecret string   `json:"shared_secret"`
	RemoteSeeds  []string `json:"remote_seeds"`
	AdvertiseURL string   `json:"advertise_url"`
	// Mode: "push" (spoke uplink), "receive" (hub) or "both" (legacy pair).
	Mode string `json:"mode"`
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
	seeds := make([]string, 0, len(req.RemoteSeeds))
	for _, sd := range req.RemoteSeeds {
		if v := strings.TrimSpace(sd); v != "" {
			seeds = append(seeds, v)
		}
	}
	// Push / two-way modes ship to the seeds: applying without a hub URL
	// would silently stall the uplink ("no healthy peer URL").
	if mode := strings.ToLower(strings.TrimSpace(req.Mode)); mode != "receive" && len(seeds) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "hub URL is required in this mode — the sender ships to the remote seed URLs"})
		return
	}
	if err := h.bridgeCfg.SaveBridge(req.SharedSecret, seeds, req.AdvertiseURL, req.Mode); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"saved": true})
}

// GetBridgeSecret returns the configured cross-cluster HMAC secret so the
// hub admin can copy it when enrolling a new site — the form field is
// otherwise write-only and the value is gone after a page reload.
// Admin-only: the same trust level that sets the secret via POST /raft/bridge.
//
// GET /api/v1/raft/bridge/secret
func (h *Handler) GetBridgeSecret(c *gin.Context) {
	if h.bridgeCfg == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "bridge configurator not wired"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"shared_secret": h.bridgeCfg.BridgeSecret()})
}

// GetBridgeConfig returns the saved uplink configuration (mode, secret,
// seeds, advertise URL) so the admin form prefills current values — an
// empty form that gets re-applied would otherwise wipe the hub URL.
// Admin-only.
//
// GET /api/v1/raft/bridge
func (h *Handler) GetBridgeConfig(c *gin.Context) {
	if h.bridgeCfg == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "bridge configurator not wired"})
		return
	}
	mode, secret, seeds, advertiseURL := h.bridgeCfg.BridgeSettings()
	if seeds == nil {
		seeds = []string{}
	}
	c.JSON(http.StatusOK, gin.H{
		"mode":          mode,
		"shared_secret": secret,
		"remote_seeds":  seeds,
		"advertise_url": advertiseURL,
	})
}
