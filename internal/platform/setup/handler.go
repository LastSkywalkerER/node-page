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

// Handler handles setup-related HTTP requests
type Handler struct {
	configWriter    *ConfigWriter
	userService     users.UserService
	onSetupComplete func() // called once after setup finishes; may be nil

	// raftSvc is optional: set when RAFT_ENABLED=true. It is consulted by
	// the JoinRaftCluster endpoint to forward a join token to the peer
	// cluster's leader.
	raftSvc      raftcluster.Service
	raftCluster  string
	raftNode     string
	raftBindAddr string
}

// NewHandler creates a new setup handler
func NewHandler(configWriter *ConfigWriter, userService users.UserService, onSetupComplete func()) *Handler {
	return &Handler{
		configWriter:    configWriter,
		userService:     userService,
		onSetupComplete: onSetupComplete,
	}
}

// WithRaft wires the Raft layer so the wizard can call its "Join existing
// cluster" branch. Safe to call with svc=nil — the endpoint then 503s.
func (h *Handler) WithRaft(svc raftcluster.Service, clusterID, nodeID, advertiseAddr string) *Handler {
	h.raftSvc = svc
	h.raftCluster = clusterID
	h.raftNode = nodeID
	h.raftBindAddr = advertiseAddr
	return h
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

	// Write configuration to .env file
	if err := h.configWriter.WriteConfigFile(req.Config); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code":   "config_write_error",
			"error":  "Failed to write configuration file",
			"detail": err.Error(),
		})
		return
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

	c.JSON(http.StatusOK, gin.H{
		"data": CompleteSetupResponse{
			Message: "Setup completed successfully. Please restart the server for changes to take effect.",
		},
	})
}

// JoinRaftClusterRequest is the body of POST /setup/join-raft-cluster.
type JoinRaftClusterRequest struct {
	PeerURL string `json:"peer_url" binding:"required"`
	Token   string `json:"token"    binding:"required"`
}

// JoinRaftCluster is the wizard's "Join existing cluster" branch. It takes
// a peer URL and a one-shot join token, forwards them to the peer's
// /api/v1/raft/join endpoint with this node's id + advertise address,
// then the peer leader adds us as a voter and replicates the snapshot.
// As soon as the snapshot lands the users table fills and /setup/status
// flips to setup_needed=false — the frontend polls until it sees that
// flip and then redirects to /auth.
//
// POST /api/v1/setup/join-raft-cluster
func (h *Handler) JoinRaftCluster(c *gin.Context) {
	ctx := c.Request.Context()

	if h.raftSvc == nil || !h.raftSvc.Enabled() {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"code":  "raft_disabled",
			"error": "this node was not started with RAFT_ENABLED=true; relaunch with the cluster env vars and try again",
		})
		return
	}
	if h.raftNode == "" || h.raftBindAddr == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"code":  "raft_misconfigured",
			"error": "RAFT_NODE_ID and RAFT_ADVERTISE_ADDR (or RAFT_BIND_ADDR) are required",
		})
		return
	}

	// Same gate as CompleteSetup — refuse if users already exist locally,
	// to avoid clobbering an already-provisioned node.
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

	body, _ := json.Marshal(map[string]string{
		"token":          req.Token,
		"node_id":        h.raftNode,
		"advertise_addr": h.raftBindAddr,
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

	c.JSON(http.StatusOK, gin.H{
		"data": gin.H{
			"message":         "Join accepted; the peer cluster's leader will replicate state to this node. Watch /setup/status — it will flip to setup_needed=false once the snapshot lands.",
			"peer_response":   json.RawMessage(respBody),
		},
	})
}
