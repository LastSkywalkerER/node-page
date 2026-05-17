package setup

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"system-stats/internal/app/dockerenv"
	users "system-stats/internal/auth/users"
	raftcluster "system-stats/internal/cluster/raft"
)

// RaftActivator is the small interface the setup wizard uses to hot-init
// the Raft layer from a runtime config. The DI container satisfies it.
type RaftActivator interface {
	ActivateRaft(ctx context.Context, cfg raftcluster.RuntimeConfig) error
}

// ClusterSecretReader looks up the cluster-shared JWT signing keys from
// the replicated cluster_config table. The setup handler polls this
// after a successful Raft join so it can swap the local token service
// keys without restarting the process.
type ClusterSecretReader interface {
	LookupClusterSecret(ctx context.Context, key string) (string, error)
}

// Handler handles setup-related HTTP requests
type Handler struct {
	configWriter    *ConfigWriter
	userService     users.UserService
	onSetupComplete func() // called once after setup finishes; may be nil

	raftSvc       raftcluster.Service
	raftActivator RaftActivator
	tokenService  users.TokenService
	secretReader  ClusterSecretReader
}

// NewHandler creates a new setup handler
func NewHandler(configWriter *ConfigWriter, userService users.UserService, onSetupComplete func()) *Handler {
	return &Handler{
		configWriter:    configWriter,
		userService:     userService,
		onSetupComplete: onSetupComplete,
	}
}

// WithRaft wires the Raft service so the wizard can call its "Join existing
// cluster" branch and inspect cluster state. Safe to call with svc=nil.
func (h *Handler) WithRaft(svc raftcluster.Service) *Handler {
	h.raftSvc = svc
	return h
}

// WithRaftActivator wires the runtime activator (the DI container) so the
// wizard can hot-init the Raft layer when an operator picks "Start new
// cluster" or "Join existing cluster".
func (h *Handler) WithRaftActivator(act RaftActivator) *Handler {
	h.raftActivator = act
	return h
}

// WithTokenService wires the token service so the wizard can swap JWT /
// refresh signing keys after writing them to .env (no restart needed).
func (h *Handler) WithTokenService(ts users.TokenService) *Handler {
	h.tokenService = ts
	return h
}

// WithSecretReader wires a reader that returns cluster-shared secrets
// from the replicated cluster_config table. The join flow uses it to
// poll for jwt_secret / refresh_secret after snapshot replay.
func (h *Handler) WithSecretReader(r ClusterSecretReader) *Handler {
	h.secretReader = r
	return h
}

// pollClusterSecretsAndSwap polls the local cluster_config table for the
// jwt_secret + refresh_secret keys that arrive via Raft snapshot replay
// from the peer cluster. As soon as both are present it swaps them into
// the live token service so users can log in without a restart.
func (h *Handler) pollClusterSecretsAndSwap() {
	if h.tokenService == nil || h.secretReader == nil {
		return
	}
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		time.Sleep(2 * time.Second)
		pollCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		jwt, _ := h.secretReader.LookupClusterSecret(pollCtx, "jwt_secret")
		refresh, _ := h.secretReader.LookupClusterSecret(pollCtx, "refresh_secret")
		cancel()
		if jwt != "" && refresh != "" {
			h.tokenService.SetSecrets(jwt, refresh)
			return
		}
	}
}

// SetupStatusResponse represents the setup status response
type SetupStatusResponse struct {
	SetupNeeded     bool         `json:"setup_needed"`
	RunningInDocker bool         `json:"running_in_docker"`
	MachineHints    MachineHints `json:"machine_hints"`
}

// ConfigResponse represents the current configuration response
type ConfigResponse struct {
	Config *ConfigValues `json:"config"`
}

// CompleteSetupRequest represents the complete setup request
type CompleteSetupRequest struct {
	Config        *ConfigValues `json:"config" binding:"required"`
	AdminEmail    string        `json:"admin_email" binding:"required,email"`
	AdminPassword string        `json:"admin_password" binding:"required,min=8"`
}

// CompleteSetupResponse represents the complete setup response
type CompleteSetupResponse struct {
	Message string `json:"message"`
}

// PreviewEnvRequest is the body for POST /setup/preview-env.
type PreviewEnvRequest struct {
	Config *ConfigValues `json:"config" binding:"required"`
}

// PreviewEnvResponse returns the generated .env file text.
type PreviewEnvResponse struct {
	Content string `json:"content"`
}

// Status checks if setup is needed (no users exist)
//
// @Summary     Setup status
// @Description Returns whether initial setup is required (no users exist yet).
// @Tags        setup
// @Produce     json
// @Success     200  {object} map[string]interface{}
// @Failure     500  {object} map[string]string
// @Router      /setup/status [get]
func (h *Handler) Status(c *gin.Context) {
	ctx := c.Request.Context()

	// Check if any users exist
	count, err := h.userService.Count(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code":  "internal_error",
			"error": "Failed to check setup status",
		})
		return
	}

	setupNeeded := count == 0
	hints := MachineHints{}
	if setupNeeded {
		hints = DetectMachineHints(ctx)
	}

	c.JSON(http.StatusOK, gin.H{
		"data": SetupStatusResponse{
			SetupNeeded:     setupNeeded,
			RunningInDocker: dockerenv.Running(),
			MachineHints:    hints,
		},
	})
}

// GetConfig returns current configuration values (only if setup is needed)
//
// @Summary     Get config template
// @Description Returns the current configuration values for prefilling the setup wizard. Only available before setup is complete.
// @Tags        setup
// @Produce     json
// @Success     200  {object} map[string]interface{}
// @Failure     403  {object} map[string]string
// @Failure     500  {object} map[string]string
// @Router      /setup/config [get]
func (h *Handler) GetConfig(c *gin.Context) {
	ctx := c.Request.Context()

	// Check if setup is needed (no users exist)
	count, err := h.userService.Count(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code":  "internal_error",
			"error": "Failed to check setup status",
		})
		return
	}

	if count > 0 {
		c.JSON(http.StatusForbidden, gin.H{
			"code":  "setup_already_completed",
			"error": "Setup has already been completed",
		})
		return
	}

	// Read current configuration
	config, err := h.configWriter.ReadCurrentConfig()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code":  "internal_error",
			"error": "Failed to read configuration",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"data": ConfigResponse{
			Config: config,
		},
	})
}

// PreviewEnv renders the .env file that setup would write (for copy/paste in the wizard).
func (h *Handler) PreviewEnv(c *gin.Context) {
	ctx := c.Request.Context()

	count, err := h.userService.Count(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code":  "internal_error",
			"error": "Failed to check setup status",
		})
		return
	}

	if count > 0 {
		c.JSON(http.StatusForbidden, gin.H{
			"code":  "setup_already_completed",
			"error": "Setup has already been completed",
		})
		return
	}

	var req PreviewEnvRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"code":   "validation_error",
			"error":  "Invalid request data",
			"detail": err.Error(),
		})
		return
	}

	if req.Config == nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"code":  "validation_error",
			"error": "Config is required",
		})
		return
	}

	if req.Config.JWTSecret == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"code":  "validation_error",
			"error": "JWT_SECRET is required",
		})
		return
	}

	if req.Config.RefreshSecret == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"code":  "validation_error",
			"error": "REFRESH_SECRET is required",
		})
		return
	}

	content, err := h.configWriter.FormatEnvFile(req.Config)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"code":  "validation_error",
			"error": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"data": PreviewEnvResponse{
			Content: content,
		},
	})
}

// CompleteSetup completes the setup process
//
// @Summary     Complete setup
// @Description Writes the .env config file and creates the first admin user. Only works when no users exist.
// @Tags        setup
// @Accept      json
// @Produce     json
// @Param       body  body      CompleteSetupRequest  true  "Setup configuration and admin credentials"
// @Success     200   {object}  map[string]interface{}
// @Failure     400   {object}  map[string]string
// @Failure     403   {object}  map[string]string
// @Failure     409   {object}  map[string]string
// @Failure     500   {object}  map[string]string
// @Router      /setup/complete [post]
func (h *Handler) CompleteSetup(c *gin.Context) {
	ctx := c.Request.Context()

	// Check if setup is needed (no users exist)
	count, err := h.userService.Count(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code":  "internal_error",
			"error": "Failed to check setup status",
		})
		return
	}

	if count > 0 {
		c.JSON(http.StatusForbidden, gin.H{
			"code":  "setup_already_completed",
			"error": "Setup has already been completed",
		})
		return
	}

	var req CompleteSetupRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"code":   "validation_error",
			"error":  "Invalid request data",
			"detail": err.Error(),
		})
		return
	}

	// Validate required config fields
	if req.Config == nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"code":  "validation_error",
			"error": "Config is required",
		})
		return
	}

	if req.Config.JWTSecret == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"code":  "validation_error",
			"error": "JWT_SECRET is required",
		})
		return
	}

	if req.Config.RefreshSecret == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"code":  "validation_error",
			"error": "REFRESH_SECRET is required",
		})
		return
	}

	ApplySetupDefaults(req.Config)
	ApplyRaftDefaults(req.Config)

	// Write configuration to .env file
	if err := h.configWriter.WriteConfigFile(req.Config); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code":   "config_write_error",
			"error":  "Failed to write configuration file",
			"detail": err.Error(),
		})
		return
	}

	// Hot-swap JWT signing keys so the token service starts using the
	// wizard-supplied secrets immediately (no restart needed for
	// login to work right after wizard completion).
	if h.tokenService != nil {
		h.tokenService.SetSecrets(req.Config.JWTSecret, req.Config.RefreshSecret)
	}

	// Optionally hot-activate Raft if the operator picked "Start new
	// cluster" in the wizard. Single-voter bootstrap is the only mode
	// we support here — other voters join later via /raft/join.
	if strings.ToLower(strings.TrimSpace(req.Config.RaftEnabled)) == "true" && h.raftActivator != nil {
		rt := raftcluster.RuntimeConfig{
			ClusterID:          strings.TrimSpace(req.Config.RaftClusterID),
			NodeID:             strings.TrimSpace(req.Config.RaftNodeID),
			BindAddr:           strings.TrimSpace(req.Config.RaftBindAddr),
			AdvertiseAddr:      strings.TrimSpace(req.Config.RaftAdvertiseAddr),
			DataDir:            strings.TrimSpace(req.Config.RaftDataDir),
			Bootstrap:          strings.ToLower(strings.TrimSpace(req.Config.RaftBootstrap)) == "true",
			AdvertiseURL:       strings.TrimSpace(req.Config.RaftAdvertisePublicURL),
			BridgeEnabled:      strings.ToLower(strings.TrimSpace(req.Config.RaftBridgeEnabled)) == "true",
			BridgeSharedSecret: strings.TrimSpace(req.Config.RaftBridgeSharedSecret),
			BridgeRemoteSeeds:  splitCSV(req.Config.RaftBridgeRemoteSeeds),
		}
		actCtx, actCancel := context.WithTimeout(ctx, 15*time.Second)
		err := h.raftActivator.ActivateRaft(actCtx, rt)
		actCancel()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{
				"code":   "raft_activation_failed",
				"error":  raftActivationUserMsg(err, rt.BindAddr),
				"detail": err.Error(),
			})
			return
		}
	}

	// Create first admin user
	user, err := h.userService.Register(ctx, req.AdminEmail, req.AdminPassword, nil)
	if err != nil {
		// Try to clean up .env file if user creation fails
		// (but don't fail if cleanup fails)
		_ = h.configWriter.WriteConfigFile(&ConfigValues{
			JWTSecret:               "",
			RefreshSecret:           "",
			Addr:                    req.Config.Addr,
			GinMode:                 req.Config.GinMode,
			Debug:                   req.Config.Debug,
			DBType:                  req.Config.DBType,
			DBDSN:                   req.Config.DBDSN,
			PrometheusEnabled:       req.Config.PrometheusEnabled,
			PrometheusAuth:          req.Config.PrometheusAuth,
			PrometheusToken:         req.Config.PrometheusToken,
			DockerHostMetricsCompat: req.Config.DockerHostMetricsCompat,
			NodeStatsHostname:       req.Config.NodeStatsHostname,
			NodeStatsIPv4:           req.Config.NodeStatsIPv4,
		})

		status := http.StatusInternalServerError
		code := "internal_error"
		errorMsg := "Failed to create admin user"

		if strings.Contains(err.Error(), "already exists") {
			status = http.StatusConflict
			code = "email_already_exists"
			errorMsg = "User with this email already exists"
		} else if strings.Contains(err.Error(), "password") {
			status = http.StatusBadRequest
			code = "validation_error"
			errorMsg = err.Error()
		}

		c.JSON(status, gin.H{
			"code":  code,
			"error": errorMsg,
		})
		return
	}

	// Success - user created; kick off metrics collection if registered
	_ = user
	if h.onSetupComplete != nil {
		go h.onSetupComplete()
	}

	msg := "Setup completed."
	if h.raftActivator != nil && strings.ToLower(strings.TrimSpace(req.Config.RaftEnabled)) == "true" {
		msg += " Raft cluster sync is now active — open the admin panel to issue join tokens for additional nodes."
	} else {
		msg += " Tokens are signed with the new secrets immediately; no restart is required for login."
	}

	c.JSON(http.StatusOK, gin.H{
		"data": CompleteSetupResponse{Message: msg},
	})
}

// JoinRaftClusterRequest is the body of POST /setup/join-raft-cluster.
type JoinRaftClusterRequest struct {
	PeerURL string `json:"peer_url" binding:"required"`
	Token   string `json:"token"    binding:"required"`

	// Raft node parameters chosen by the wizard. All fall back to
	// sensible defaults when empty (see ApplyRaftDefaults).
	NodeID        string `json:"node_id"`
	BindAddr      string `json:"bind_addr"`
	AdvertiseAddr string `json:"advertise_addr"`
	AdvertiseURL  string `json:"advertise_url"`
	DataDir       string `json:"data_dir"`

	// Optional bridge config — when set, the joining node immediately
	// starts shipping its cluster's log to the peer cluster.
	BridgeSharedSecret string   `json:"bridge_shared_secret"`
	BridgeRemoteSeeds  []string `json:"bridge_remote_seeds"`
}

// JoinRaftCluster is the wizard's "Join existing cluster" branch. It:
//
//   1. Refuses if any users already exist locally (would clobber state).
//   2. Probes the peer's /api/v1/raft/ping to learn its cluster id.
//   3. Hot-activates the local Raft node (bootstrap=false) so it can
//      receive snapshot + log from the peer leader.
//   4. POSTs to the peer's /api/v1/raft/join with this node's id +
//      advertise address + the one-shot token. The peer adds us as a
//      voter and the snapshot starts replicating immediately.
//   5. Writes the Raft config into .env so the node survives a restart.
//
// As soon as snapshot replication finishes, the local users table fills,
// /setup/status flips to setup_needed=false, the JWT secrets land in
// cluster_config and a poller in this handler swaps them into the token
// service — at which point the frontend redirects to /auth.
//
// POST /api/v1/setup/join-raft-cluster
func (h *Handler) JoinRaftCluster(c *gin.Context) {
	ctx := c.Request.Context()

	if h.raftActivator == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"code":  "raft_unavailable",
			"error": "the Raft activator is not wired; rebuild the binary",
		})
		return
	}

	// Refuse if this node was already provisioned.
	count, err := h.userService.Count(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": "internal_error", "error": "Failed to check setup status"})
		return
	}
	if count > 0 {
		c.JSON(http.StatusForbidden, gin.H{"code": "setup_already_completed", "error": "Setup has already been completed; join is not allowed"})
		return
	}

	var req JoinRaftClusterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": "validation_error", "error": "Invalid request data", "detail": err.Error()})
		return
	}

	peerURL := strings.TrimSuffix(strings.TrimSpace(req.PeerURL), "/")
	if peerURL == "" {
		c.JSON(http.StatusBadRequest, gin.H{"code": "validation_error", "error": "peer_url is required"})
		return
	}

	// Auto-fill node-level fields when the wizard didn't provide them.
	hostHint := DetectMachineHints(ctx)
	if req.NodeID == "" {
		req.NodeID = defaultNodeID(hostHint.SuggestedHostname)
	}
	if req.BindAddr == "" {
		req.BindAddr = ":7000"
	}
	if req.AdvertiseAddr == "" {
		if hostHint.SuggestedIPv4 != "" {
			req.AdvertiseAddr = hostHint.SuggestedIPv4 + ":7000"
		} else {
			req.AdvertiseAddr = req.BindAddr
		}
	}
	if req.DataDir == "" {
		req.DataDir = "./data/raft"
	}

	// Probe the peer first to discover the cluster id we're joining
	// (so we don't ask the operator to retype it).
	clusterID, err := probePeerClusterID(ctx, peerURL)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{
			"code":   "peer_unreachable",
			"error":  peerProbeUserMsg(err, peerURL),
			"detail": err.Error(),
		})
		return
	}

	// Activate the local Raft node so it can accept replication from
	// the peer leader. Bootstrap=false — we are joining, not creating.
	rt := raftcluster.RuntimeConfig{
		ClusterID:          clusterID,
		NodeID:             req.NodeID,
		BindAddr:           req.BindAddr,
		AdvertiseAddr:      req.AdvertiseAddr,
		DataDir:            req.DataDir,
		Bootstrap:          false,
		AdvertiseURL:       req.AdvertiseURL,
		BridgeSharedSecret: req.BridgeSharedSecret,
		BridgeRemoteSeeds:  req.BridgeRemoteSeeds,
		BridgeEnabled:      req.BridgeSharedSecret != "",
	}
	actCtx, actCancel := context.WithTimeout(ctx, 15*time.Second)
	if err := h.raftActivator.ActivateRaft(actCtx, rt); err != nil {
		actCancel()
		c.JSON(http.StatusInternalServerError, gin.H{
			"code":   "raft_activation_failed",
			"error":  "Raft layer could not be activated",
			"detail": err.Error(),
		})
		return
	}
	actCancel()

	// Persist the Raft config in .env so restarts come back as a voter.
	cv, _ := h.configWriter.ReadCurrentConfig()
	if cv == nil {
		cv = &ConfigValues{}
	}
	cv.RaftEnabled = "true"
	cv.RaftClusterID = clusterID
	cv.RaftNodeID = req.NodeID
	cv.RaftBindAddr = req.BindAddr
	cv.RaftAdvertiseAddr = req.AdvertiseAddr
	cv.RaftDataDir = req.DataDir
	cv.RaftBootstrap = "false"
	cv.RaftAdvertisePublicURL = req.AdvertiseURL
	if req.BridgeSharedSecret != "" {
		cv.RaftBridgeEnabled = "true"
		cv.RaftBridgeSharedSecret = req.BridgeSharedSecret
	}
	if len(req.BridgeRemoteSeeds) > 0 {
		cv.RaftBridgeRemoteSeeds = strings.Join(req.BridgeRemoteSeeds, ",")
	}
	// JWT_SECRET / REFRESH_SECRET arrive from the cluster via snapshot;
	// stub them out so FormatEnvFile doesn't refuse the write.
	if cv.JWTSecret == "" {
		cv.JWTSecret = "joining-cluster-placeholder"
	}
	if cv.RefreshSecret == "" {
		cv.RefreshSecret = "joining-cluster-placeholder"
	}
	if werr := h.configWriter.WriteConfigFile(cv); werr != nil {
		// Not fatal — Raft is already running; just log.
		_ = werr
	}

	// Tell the peer we want in.
	body, _ := json.Marshal(map[string]string{
		"token":          req.Token,
		"node_id":        req.NodeID,
		"advertise_addr": req.AdvertiseAddr,
	})
	httpCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	httpReq, err := http.NewRequestWithContext(httpCtx, http.MethodPost, peerURL+"/api/v1/raft/join", bytes.NewReader(body))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": "internal_error", "error": err.Error()})
		return
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(httpReq)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"code": "peer_unreachable", "error": err.Error()})
		return
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		c.JSON(http.StatusBadGateway, gin.H{
			"code":   "peer_rejected",
			"error":  fmt.Sprintf("peer returned %s", resp.Status),
			"detail": string(respBody),
		})
		return
	}

	// Once snapshot replay lands the cluster-shared JWT signing keys, swap
	// them into the token service so logins succeed immediately. Best-effort
	// background poll for up to ~2 min; if it times out, a restart still
	// works (the env file was updated above).
	if h.tokenService != nil && h.secretReader != nil {
		go h.pollClusterSecretsAndSwap()
	}

	c.JSON(http.StatusOK, gin.H{
		"data": gin.H{
			"message":       "Join accepted. Snapshot replication is starting; the wizard will redirect to /auth once the first admin user lands.",
			"cluster_id":    clusterID,
			"peer_response": json.RawMessage(respBody),
		},
	})
}

// defaultNodeID derives a stable, human-readable node id from the host's
// hostname when the wizard doesn't provide one explicitly.
func defaultNodeID(hostname string) string {
	h := strings.ToLower(strings.TrimSpace(hostname))
	if h == "" {
		return "node-1"
	}
	return strings.ReplaceAll(h, " ", "-")
}

// probePeerClusterID GETs /api/v1/raft/ping on peerURL and reads the
// X-Raft-Cluster-ID response header.
func probePeerClusterID(ctx context.Context, peerURL string) (string, error) {
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(pingCtx, http.MethodGet, peerURL+"/api/v1/raft/ping", nil)
	if err != nil {
		return "", err
	}
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return "", fmt.Errorf("peer ping returned %s", resp.Status)
	}
	cid := strings.TrimSpace(resp.Header.Get("X-Raft-Cluster-ID"))
	if cid == "" {
		return "", fmt.Errorf("peer did not return X-Raft-Cluster-ID header")
	}
	return cid, nil
}

func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if v := strings.TrimSpace(p); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// peerProbeUserMsg recognises the common mistake of pasting the Raft TCP
// transport URL (port 7000) instead of the HTTP API URL (port 8080) and
// returns a short, actionable explanation. Everything else falls back to
// a generic "could not reach peer" line; the raw Go error is still
// available in the response's "detail" field for debugging.
func peerProbeUserMsg(err error, peerURL string) string {
	if err == nil {
		return "could not reach peer /raft/ping"
	}
	msg := err.Error()
	if strings.Contains(msg, "EOF") || strings.Contains(msg, "malformed HTTP response") {
		return "the peer URL points at the Raft TCP transport port (binary protocol), not the HTTP API. " +
			"Use the same URL you open in a browser — typically port 8080 (e.g. http://192.168.0.104:8080)."
	}
	if strings.Contains(msg, "connection refused") {
		return "the peer is not accepting HTTP connections on " + peerURL + ". " +
			"Check the URL (use the HTTP API port, typically 8080) and that the cluster leader is running."
	}
	if strings.Contains(msg, "no such host") || strings.Contains(msg, "no route to host") {
		return "could not resolve / route to " + peerURL + ". Check the hostname / IP and that this node can reach it."
	}
	if strings.Contains(msg, "did not return X-Raft-Cluster-ID") {
		return "the peer URL responds to HTTP but is not a node-stats Raft node " +
			"(missing X-Raft-Cluster-ID header). Double-check the URL."
	}
	return "could not reach peer /raft/ping at " + peerURL
}

// raftActivationUserMsg turns the raw activation error into a one-line,
// user-actionable message for the wizard's "error" field. The full
// stack-trace-style detail stays in the "detail" field for debugging.
func raftActivationUserMsg(err error, bindAddr string) string {
	if err == nil {
		return "Raft layer could not be activated"
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "address already in use"):
		return fmt.Sprintf(
			"Raft port %s is already in use. Pick a different port (e.g. 17000) or stop the process holding it (lsof -i %s on the host).",
			bindAddr, bindAddr,
		)
	case strings.Contains(msg, "permission denied"):
		return fmt.Sprintf(
			"Raft cannot bind %s: permission denied. Ports below 1024 typically require root; pick a port above 1024.",
			bindAddr,
		)
	case strings.Contains(msg, "cannot assign requested address"):
		return "Raft cannot bind the configured address: this host doesn't expose that interface. Check the 'Advertise host' field."
	default:
		return "Raft layer could not be activated"
	}
}
