package setup

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	appconfig "system-stats/internal/app/config"
	"system-stats/internal/app/dockerenv"
	users "system-stats/internal/auth/users"
	raftcluster "system-stats/internal/cluster/raft"
)

// RaftActivator is the small interface the setup wizard uses to hot-init
// the Raft layer from a runtime config. The DI container satisfies it.
type RaftActivator interface {
	ActivateRaft(ctx context.Context, cfg raftcluster.RuntimeConfig) error
	// ShutdownAndWipeRaft tears down the running Raft node and clears
	// on-disk state without re-activating. Used by the join flow when
	// the local node is already half-configured from a previous failed
	// attempt; without wiping, ActivateRaft early-returns and the new
	// parameters from the wizard form would be silently ignored.
	ShutdownAndWipeRaft() error
	// RaftEnabled reports whether the running raft layer is active.
	RaftEnabled() bool
	// SeedClusterSecrets publishes the local auth secrets into the cluster
	// (leader-only) so future joiners authenticate. No-op when not leader.
	SeedClusterSecrets(ctx context.Context, jwtSecret, refreshSecret string) error
	// AdvertiseSelfNow publishes this node's advertise URL into the catalog
	// (best-effort, leader-only) so followers can forward writes.
	AdvertiseSelfNow(ctx context.Context)
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

	raftSvc         raftcluster.Service
	raftActivator   RaftActivator
	tokenService    users.TokenService
	secretReader    ClusterSecretReader
	metricSecretSet func(string) // pushes the cluster JWT key into the metric stream
	addr            string       // local HTTP ADDR (":8080") used to derive default http_url
}

// WithHTTPAddr records the local HTTP listen address ("ADDR" env) so the
// join flow can derive a sensible default http_url for itself when the
// operator didn't fill RAFT_ADVERTISE_PUBLIC_URL.
func (h *Handler) WithHTTPAddr(addr string) *Handler {
	h.addr = addr
	return h
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

// WithMetricSecretSetter wires a hook (the DI container's SetClusterHMACSecret)
// so the join flow can push the discovered cluster JWT key into the off-Raft
// metric stream live — otherwise a freshly-joined node keeps HMACing its metrics
// with the boot placeholder secret and peers reject them until a restart.
func (h *Handler) WithMetricSecretSetter(fn func(string)) *Handler {
	h.metricSecretSet = fn
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
			// Also retune the off-Raft metric stream to the cluster key so
			// cross-node metrics start flowing without waiting for a restart.
			if h.metricSecretSet != nil {
				h.metricSecretSet(jwt)
			}
			return
		}
	}
}

// SetupStatusResponse represents the setup status response
type SetupStatusResponse struct {
	SetupNeeded     bool `json:"setup_needed"`
	RunningInDocker bool `json:"running_in_docker"`
	// ManagedExternally is true when an external orchestrator (Dokploy/Traefik)
	// owns the stack lifecycle, so the wizard hides the managed-Postgres option
	// and won't trigger a compose recreate.
	ManagedExternally bool         `json:"managed_externally"`
	MachineHints      MachineHints `json:"machine_hints"`
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
	// RestartPending is true when applying the chosen configuration requires the
	// docker stack to be recreated by the controller (DB engine change). The
	// frontend then shows an "applying & restarting" gate and polls /health +
	// /version until the new backend answers before redirecting to login.
	RestartPending bool   `json:"restart_pending"`
	DBMode         string `json:"db_mode,omitempty"`
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
			SetupNeeded:       setupNeeded,
			RunningInDocker:   dockerenv.Running(),
			ManagedExternally: ManagedExternally(),
			MachineHints:      hints,
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

	// Starting a cluster via the wizard: derive the leader's HTTP advertise URL
	// from its Raft advertise host + this node's HTTP port when the operator left
	// it blank. Without it the leader has no published API URL, so peers can't
	// reach it to join / forward writes, and the connect-key panel shows a wrong
	// default port (the admin/join paths derive this; CompleteSetup didn't).
	if strings.EqualFold(strings.TrimSpace(req.Config.RaftEnabled), "true") &&
		strings.TrimSpace(req.Config.RaftAdvertisePublicURL) == "" {
		if u := deriveJoinerHTTPURL(strings.TrimSpace(req.Config.RaftAdvertiseAddr), h.addr); u != "" {
			req.Config.RaftAdvertisePublicURL = u
		}
	}

	// Managed Postgres: reuse the previously-generated password if one exists.
	// The pgdata volume keeps the password from its FIRST init, so regenerating
	// it on a wizard retry (or the two-phase re-submit) would cause an auth
	// mismatch and crash-loop the app. The desired-state descriptor is the
	// persisted source of truth for that password.
	if strings.EqualFold(req.Config.DBType, "postgres") && req.Config.DBManaged && strings.TrimSpace(req.Config.DBPassword) == "" {
		if prev, _ := ReadDesiredState(desiredStateDir()); prev != nil &&
			prev.DBMode == DBModePostgresManaged && strings.TrimSpace(prev.DB.Password) != "" {
			req.Config.DBPassword = prev.DB.Password
		}
	}

	// For managed Postgres, force the connection to target the compose `db`
	// service and generate a password if none exists yet. Done before
	// WriteConfigFile so the persisted DSN is consistent.
	if err := finalizeManagedPostgres(req.Config); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code":   "internal_error",
			"error":  "Failed to prepare managed database configuration",
			"detail": err.Error(),
		})
		return
	}

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
		// As the bootstrap leader, seed the cluster-shared signing keys into the
		// replicated cluster_config (so joiners receive valid secrets via
		// snapshot — otherwise their token service stays unconfigured and login
		// returns auth_not_configured) and advertise our HTTP URL (so followers
		// can forward writes to us). Both are leader-only, and a synchronous call
		// races the just-started bootstrap election — so wait for leadership in a
		// background goroutine first. Mirrors AdminStartCluster. Best-effort.
		if rt.Bootstrap {
			jwt, refresh := req.Config.JWTSecret, req.Config.RefreshSecret
			go func() {
				for i := 0; i < 50; i++ { // wait up to ~10s for the election
					if h.raftSvc != nil && strings.EqualFold(h.raftSvc.Status().State, "Leader") {
						break
					}
					time.Sleep(200 * time.Millisecond)
				}
				sctx, scancel := context.WithTimeout(context.Background(), 10*time.Second)
				_ = h.raftActivator.SeedClusterSecrets(sctx, jwt, refresh)
				scancel()
				h.raftActivator.AdvertiseSelfNow(context.Background())
			}()
		}
	}

	// If the chosen database engine differs from the one this process is running
	// on (docker only), the controller must recreate the stack onto the new DB
	// before we can create the admin user — the new database is empty, so
	// creating the admin now would land it in the OLD database and be lost.
	// We persist the desired topology and return restart_pending; the frontend
	// re-submits this same request once the recreated backend is healthy, at
	// which point running == desired and the admin is created on the new DB.
	if mode, needed := dbSwitchNeeded(req.Config); needed {
		if err := h.writeDesiredState(mode, AssembleDSN(req.Config), req.Config); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{
				"code":   "deploy_state_error",
				"error":  "Failed to record the deployment state for the controller",
				"detail": err.Error(),
			})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"data": CompleteSetupResponse{
				Message:        "Provisioning the database and restarting — this can take a moment.",
				RestartPending: true,
				DBMode:         mode,
			},
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

// finalizeManagedPostgres forces a managed-Postgres config to target the
// compose `db` service and fills missing fields (generating a password if the
// wizard didn't). No-op for sqlite / external Postgres.
func finalizeManagedPostgres(cv *ConfigValues) error {
	if !strings.EqualFold(cv.DBType, "postgres") || !cv.DBManaged {
		return nil
	}
	cv.DBHost = "db"
	if cv.DBPort == "" {
		cv.DBPort = "5432"
	}
	if cv.DBSSLMode == "" {
		cv.DBSSLMode = "disable"
	}
	if cv.DBName == "" {
		cv.DBName = "node_stats"
	}
	if cv.DBUser == "" {
		cv.DBUser = "node_stats"
	}
	if strings.TrimSpace(cv.DBPassword) == "" {
		pw, err := randomHex(24)
		if err != nil {
			return err
		}
		cv.DBPassword = pw
	}
	return nil
}

// dbSwitchNeeded reports the desired DB mode and whether applying it requires
// the controller to recreate the stack onto a different engine than the one
// this process is currently running. Only relevant in Docker, and never when
// the deployment is managed externally.
func dbSwitchNeeded(cv *ConfigValues) (mode string, needed bool) {
	mode = DBModeSQLite
	desiredEngine := "sqlite"
	if strings.EqualFold(cv.DBType, "postgres") {
		desiredEngine = "postgres"
		if cv.DBManaged {
			mode = DBModePostgresManaged
		} else {
			mode = DBModePostgresExternal
		}
	}
	runningEngine := strings.ToLower(strings.TrimSpace(os.Getenv("DB_TYPE")))
	if runningEngine == "" {
		runningEngine = "sqlite"
	}
	needed = dockerenv.Running() && !ManagedExternally() && desiredEngine != runningEngine
	return mode, needed
}

// writeDesiredState records the target topology for the controller to apply.
func (h *Handler) writeDesiredState(mode, dsn string, cv *ConfigValues) error {
	dir := desiredStateDir()
	gen := 1
	if prev, _ := ReadDesiredState(dir); prev != nil {
		gen = prev.Generation + 1
	}
	ds := DesiredState{Generation: gen, DBMode: mode, DBDSN: dsn}
	if mode == DBModePostgresManaged {
		ds.DB = DBProvision{Name: cv.DBName, User: cv.DBUser, Password: cv.DBPassword}
	}
	return WriteDesiredState(dir, ds)
}

// desiredStateDir is the shared data dir where the app writes desired-state.json
// for the controller to read (the app's /app/data mount in Docker).
func desiredStateDir() string {
	if d := strings.TrimSpace(os.Getenv("NODE_STATS_DATA_DIR")); d != "" {
		return d
	}
	return "/app/data"
}

// randomHex returns n random bytes hex-encoded (2n chars).
func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// TestDBRequest is the body of POST /setup/db/test.
type TestDBRequest struct {
	DBDSN      string `json:"db_dsn"`
	DBHost     string `json:"db_host"`
	DBPort     string `json:"db_port"`
	DBName     string `json:"db_name"`
	DBUser     string `json:"db_user"`
	DBPassword string `json:"db_password"`
	DBSSLMode  string `json:"db_sslmode"`
}

// TestDB opens a short-lived connection to an EXTERNAL Postgres to validate the
// credentials before the operator commits to them. It never persists or
// migrates anything. Public, but only usable pre-setup (no users yet).
//
// POST /api/v1/setup/db/test
func (h *Handler) TestDB(c *gin.Context) {
	ctx := c.Request.Context()
	count, err := h.userService.Count(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": "internal_error", "error": "Failed to check setup status"})
		return
	}
	if count > 0 {
		c.JSON(http.StatusForbidden, gin.H{"code": "setup_already_completed", "error": "Setup has already been completed"})
		return
	}

	var req TestDBRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": "validation_error", "error": "Invalid request data", "detail": err.Error()})
		return
	}

	dsn := strings.TrimSpace(req.DBDSN)
	if dsn == "" {
		dsn = AssembleDSN(&ConfigValues{
			DBType: "postgres", DBHost: req.DBHost, DBPort: req.DBPort, DBName: req.DBName,
			DBUser: req.DBUser, DBPassword: req.DBPassword, DBSSLMode: req.DBSSLMode,
		})
	}
	if strings.TrimSpace(dsn) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"code": "validation_error", "error": "a host or full DSN is required"})
		return
	}

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err == nil {
		if raw, derr := db.DB(); derr == nil {
			err = raw.PingContext(pingCtx)
			_ = raw.Close()
		} else {
			err = derr
		}
	}
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"data": gin.H{"ok": false, "error": err.Error()}})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"ok": true}})
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
//  1. Refuses if any users already exist locally (would clobber state).
//  2. Probes the peer's /api/v1/raft/ping to learn its cluster id.
//  3. Hot-activates the local Raft node (bootstrap=false) so it can
//     receive snapshot + log from the peer leader.
//  4. POSTs to the peer's /api/v1/raft/join with this node's id +
//     advertise address + the one-shot token. The peer adds us as a
//     voter and the snapshot starts replicating immediately.
//  5. Writes the Raft config into .env so the node survives a restart.
//
// As soon as snapshot replication finishes, the local users table fills,
// /setup/status flips to setup_needed=false, the JWT secrets land in
// cluster_config and a poller in this handler swaps them into the token
// service — at which point the frontend redirects to /auth.
//
// POST /api/v1/setup/join-raft-cluster
func (h *Handler) JoinRaftCluster(c *gin.Context) {
	ctx := c.Request.Context()

	// Refuse if this node was already provisioned (the wizard path is for
	// fresh nodes only; post-setup join is the admin AdminJoinCluster path,
	// which requires an explicit data-loss acknowledgement).
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

	status, body := h.performRaftJoin(ctx, req)
	c.JSON(status, body)
}

// AdminJoinClusterRequest is the body of the authenticated POST /cluster/join.
// It is the wizard's join request plus an explicit data-loss acknowledgement,
// because joining replaces this node's entire local state (users, metrics,
// settings) with the cluster's snapshot.
type AdminJoinClusterRequest struct {
	JoinRaftClusterRequest
	AcknowledgeDataLoss bool `json:"acknowledge_data_loss"`
}

// AdminJoinCluster lets an authenticated admin attach an already-provisioned
// node to an existing cluster (the post-setup analogue of the wizard's join).
// It does NOT refuse on count>0 — instead it requires acknowledge_data_loss
// because the incoming snapshot wipes and replaces all managed tables. The
// admin is logged out once the cluster secrets replace the local ones.
//
// POST /api/v1/cluster/join  (AuthJWT + admin)
func (h *Handler) AdminJoinCluster(c *gin.Context) {
	ctx := c.Request.Context()

	var req AdminJoinClusterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": "validation_error", "error": "Invalid request data", "detail": err.Error()})
		return
	}
	if !req.AcknowledgeDataLoss {
		c.JSON(http.StatusBadRequest, gin.H{
			"code":  "ack_required",
			"error": "joining a cluster replaces all local data with the cluster's state; set acknowledge_data_loss=true to proceed",
		})
		return
	}

	status, body := h.performRaftJoin(ctx, req.JoinRaftClusterRequest)
	c.JSON(status, body)
}

// performRaftJoin runs the shared "join an existing cluster" sequence used by
// both the public setup wizard (JoinRaftCluster) and the authenticated admin
// action (AdminJoinCluster). It returns the HTTP status and response body to
// send. It assumes the caller already gate-checked provisioning/acknowledgement.
func (h *Handler) performRaftJoin(ctx context.Context, req JoinRaftClusterRequest) (int, gin.H) {
	if h.raftActivator == nil {
		return http.StatusServiceUnavailable, gin.H{
			"code":  "raft_unavailable",
			"error": "the Raft activator is not wired; rebuild the binary",
		}
	}

	peerURL := strings.TrimSuffix(strings.TrimSpace(req.PeerURL), "/")
	if peerURL == "" {
		return http.StatusBadRequest, gin.H{"code": "validation_error", "error": "peer_url is required"}
	}

	// Auto-fill node-level fields when the caller didn't provide them.
	hostHint := DetectMachineHints(ctx)
	if req.NodeID == "" {
		req.NodeID = defaultNodeID(hostHint.SuggestedHostname)
	}
	if req.BindAddr == "" {
		req.BindAddr = ":" + defaultRaftPort()
	}
	if req.AdvertiseAddr == "" {
		if hostHint.SuggestedIPv4 != "" {
			req.AdvertiseAddr = hostHint.SuggestedIPv4 + ":" + defaultRaftPort()
		} else {
			req.AdvertiseAddr = req.BindAddr
		}
	}
	if req.DataDir == "" {
		req.DataDir = appconfig.DefaultRaftDataDir()
	}

	// Validate the Raft addresses BEFORE activation so a malformed value
	// (e.g. a stray URL pasted into an advanced field) returns a clean 400
	// instead of bubbling up from net.ResolveTCPAddr as a 500
	// raft_activation_failed. bind may be ":port"; advertise must resolve to
	// a concrete host:port the peer can dial.
	if _, _, err := net.SplitHostPort(req.BindAddr); err != nil {
		return http.StatusBadRequest, gin.H{
			"code":   "validation_error",
			"error":  fmt.Sprintf("invalid Raft bind address %q — expected host:port like \":7000\"", req.BindAddr),
			"detail": err.Error(),
		}
	}
	if _, err := net.ResolveTCPAddr("tcp", req.AdvertiseAddr); err != nil {
		return http.StatusBadRequest, gin.H{
			"code":   "validation_error",
			"error":  fmt.Sprintf("invalid Raft advertise address %q — expected host:port like \"10.0.0.2:7000\"", req.AdvertiseAddr),
			"detail": err.Error(),
		}
	}

	// Probe the peer first to discover the cluster id we're joining
	// (so we don't ask the operator to retype it).
	clusterID, err := probePeerClusterID(ctx, peerURL)
	if err != nil {
		return http.StatusBadGateway, gin.H{
			"code":   "peer_unreachable",
			"error":  peerProbeUserMsg(err, peerURL),
			"detail": err.Error(),
		}
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
	// If this node has a half-configured Raft layer from a previous
	// failed join attempt (e.g. .env had RAFT_ENABLED=true with a stale
	// bind addr), wipe it first so the wizard's new params actually
	// take effect. Without this, activateLocked early-returns because
	// swap.Enabled() is already true and the user wonders why nothing
	// changed.
	if h.raftActivator.RaftEnabled() {
		if err := h.raftActivator.ShutdownAndWipeRaft(); err != nil {
			return http.StatusInternalServerError, gin.H{
				"code":   "raft_wipe_failed",
				"error":  "could not reset the existing Raft state before joining",
				"detail": err.Error(),
			}
		}
	}

	actCtx, actCancel := context.WithTimeout(ctx, 15*time.Second)
	if err := h.raftActivator.ActivateRaft(actCtx, rt); err != nil {
		actCancel()
		return http.StatusInternalServerError, gin.H{
			"code":   "raft_activation_failed",
			"error":  raftActivationUserMsg(err, rt.BindAddr),
			"detail": err.Error(),
		}
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

	// Tell the peer we want in. http_url is the joiner's externally
	// reachable HTTP base URL; the leader publishes it via
	// CmdPeerNodeAdvertise so SubmitCommand forwarding works when this
	// node later sits as a follower trying to write.
	httpURL := req.AdvertiseURL
	if httpURL == "" {
		httpURL = deriveJoinerHTTPURL(req.AdvertiseAddr, h.addr)
	}
	body, _ := json.Marshal(map[string]string{
		"token":          req.Token,
		"node_id":        req.NodeID,
		"advertise_addr": req.AdvertiseAddr,
		"http_url":       httpURL,
	})
	httpCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	httpReq, err := http.NewRequestWithContext(httpCtx, http.MethodPost, peerURL+"/api/v1/raft/join", bytes.NewReader(body))
	if err != nil {
		return http.StatusInternalServerError, gin.H{"code": "internal_error", "error": err.Error()}
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(httpReq)
	if err != nil {
		return http.StatusBadGateway, gin.H{"code": "peer_unreachable", "error": err.Error()}
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		return http.StatusBadGateway, gin.H{
			"code":   "peer_rejected",
			"error":  fmt.Sprintf("peer returned %s", resp.Status),
			"detail": string(respBody),
		}
	}

	// Once snapshot replay lands the cluster-shared JWT signing keys, swap
	// them into the token service so logins succeed immediately. Best-effort
	// background poll for up to ~2 min; if it times out, a restart still
	// works (the env file was updated above).
	if h.tokenService != nil && h.secretReader != nil {
		go h.pollClusterSecretsAndSwap()
	}

	return http.StatusOK, gin.H{
		"data": gin.H{
			"message":       "Join accepted. Snapshot replication is starting; once the cluster's first admin lands you'll be redirected to sign in.",
			"cluster_id":    clusterID,
			"peer_response": json.RawMessage(respBody),
		},
	}
}

// StartClusterRequest is the (optional) body of POST /cluster/start. All
// fields fall back to sensible defaults derived from the host when empty.
type StartClusterRequest struct {
	ClusterID     string `json:"cluster_id"`
	NodeID        string `json:"node_id"`
	BindAddr      string `json:"bind_addr"`
	AdvertiseAddr string `json:"advertise_addr"`
	AdvertiseURL  string `json:"advertise_url"`
	DataDir       string `json:"data_dir"`
}

// AdminStartCluster converts an already-provisioned standalone node into the
// leader of a new single-voter cluster, at runtime and without a restart.
// It is the post-setup analogue of the wizard's "Start new cluster" branch.
// Non-destructive: the node keeps all its local data, which becomes the
// cluster's initial state. Other nodes then enrol via a join token.
//
// POST /api/v1/cluster/start  (AuthJWT + admin)
func (h *Handler) AdminStartCluster(c *gin.Context) {
	ctx := c.Request.Context()

	if h.raftActivator == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"code":  "raft_unavailable",
			"error": "the Raft activator is not wired; rebuild the binary",
		})
		return
	}
	if h.raftActivator.RaftEnabled() {
		c.JSON(http.StatusConflict, gin.H{
			"code":  "raft_already_active",
			"error": "this node is already part of a cluster",
		})
		return
	}

	var req StartClusterRequest
	_ = c.ShouldBindJSON(&req) // body is optional; all fields default

	hostHint := DetectMachineHints(ctx)
	clusterID := strings.TrimSpace(req.ClusterID)
	if clusterID == "" {
		clusterID = defaultClusterID(hostHint.SuggestedHostname)
	}
	nodeID := strings.TrimSpace(req.NodeID)
	if nodeID == "" {
		nodeID = defaultNodeID(hostHint.SuggestedHostname)
	}
	bindAddr := strings.TrimSpace(req.BindAddr)
	if bindAddr == "" {
		bindAddr = ":" + defaultRaftPort()
	}
	advertiseAddr := strings.TrimSpace(req.AdvertiseAddr)
	if advertiseAddr == "" {
		if hostHint.SuggestedIPv4 != "" {
			advertiseAddr = hostHint.SuggestedIPv4 + ":" + defaultRaftPort()
		} else {
			advertiseAddr = bindAddr
		}
	}
	dataDir := strings.TrimSpace(req.DataDir)
	if dataDir == "" {
		dataDir = appconfig.DefaultRaftDataDir()
	}
	advertiseURL := strings.TrimSpace(req.AdvertiseURL)

	// Validate addresses before activation (clean 400 instead of a 500).
	if _, _, err := net.SplitHostPort(bindAddr); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"code":   "validation_error",
			"error":  fmt.Sprintf("invalid Raft bind address %q — expected host:port like \":7000\"", bindAddr),
			"detail": err.Error(),
		})
		return
	}
	if _, err := net.ResolveTCPAddr("tcp", advertiseAddr); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"code":   "validation_error",
			"error":  fmt.Sprintf("invalid Raft advertise address %q — expected host:port like \"10.0.0.2:7000\"", advertiseAddr),
			"detail": err.Error(),
		})
		return
	}

	rt := raftcluster.RuntimeConfig{
		ClusterID:     clusterID,
		NodeID:        nodeID,
		BindAddr:      bindAddr,
		AdvertiseAddr: advertiseAddr,
		DataDir:       dataDir,
		Bootstrap:     true,
		AdvertiseURL:  advertiseURL,
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

	// Read the current config once: needed to seed cluster secrets and to
	// persist the Raft block back to .env.
	cv, _ := h.configWriter.ReadCurrentConfig()
	if cv == nil {
		cv = &ConfigValues{}
	}

	// Publish the live auth secrets into the cluster so nodes that later
	// join receive valid JWT signing keys via snapshot (best-effort).
	seedCtx, seedCancel := context.WithTimeout(ctx, 10*time.Second)
	_ = h.raftActivator.SeedClusterSecrets(seedCtx, cv.JWTSecret, cv.RefreshSecret)
	seedCancel()

	// Advertise self so followers can forward writes to this leader.
	h.raftActivator.AdvertiseSelfNow(ctx)

	// Persist the Raft block so the node comes back as the leader on restart.
	cv.RaftEnabled = "true"
	cv.RaftClusterID = clusterID
	cv.RaftNodeID = nodeID
	cv.RaftBindAddr = bindAddr
	cv.RaftAdvertiseAddr = advertiseAddr
	cv.RaftDataDir = dataDir
	cv.RaftBootstrap = "true"
	cv.RaftAdvertisePublicURL = advertiseURL
	if werr := h.configWriter.WriteConfigFile(cv); werr != nil {
		// Not fatal — Raft is already running; the operator can re-save later.
		_ = werr
	}

	c.JSON(http.StatusOK, gin.H{
		"data": gin.H{
			"message":        "This node is now the cluster leader. Issue a join token below to add more nodes.",
			"cluster_id":     clusterID,
			"node_id":        nodeID,
			"advertise_addr": advertiseAddr,
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

// defaultClusterID generates a per-site unique cluster id. A constant
// default ("local" historically) breaks the cross-cluster metrics uplink:
// the hub rejects batches whose sender cluster id equals its own, and the
// id keys bridge dedupe + the origin badge — every site must differ.
func defaultClusterID(hostname string) string {
	slug := defaultNodeID(hostname)
	if slug == "node-1" {
		slug = "site"
	}
	var b [3]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%s-%d", slug, time.Now().UnixNano()%0xffffff)
	}
	return slug + "-" + hex.EncodeToString(b[:])
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

// RaftProgressResponse is the minimal Raft snapshot the join-cluster
// wizard polls during the "Waiting for snapshot..." state — applied /
// commit indices + leader / state so the operator sees progress without
// being authenticated against the admin /raft/status endpoint.
type RaftProgressResponse struct {
	Enabled       bool   `json:"enabled"`
	State         string `json:"state,omitempty"`
	LeaderID      string `json:"leader_id,omitempty"`
	AppliedIndex  uint64 `json:"applied_index"`
	CommitIndex   uint64 `json:"commit_index"`
	LastIndex     uint64 `json:"last_index"`
	NodeID        string `json:"node_id,omitempty"`
	ClusterID     string `json:"cluster_id,omitempty"`
	AdvertiseAddr string `json:"advertise_addr,omitempty"`
}

// RaftProgress is the public, no-auth version of /raft/status used by
// the wizard's join-replicating panel to surface progress. It exposes
// the same information that is otherwise discoverable via the Raft TCP
// transport itself, so revealing it pre-login adds no attack surface.
//
// GET /api/v1/setup/raft-progress
func (h *Handler) RaftProgress(c *gin.Context) {
	if h.raftSvc == nil {
		c.JSON(http.StatusOK, gin.H{"data": RaftProgressResponse{Enabled: false}})
		return
	}
	st := h.raftSvc.Status()
	c.JSON(http.StatusOK, gin.H{
		"data": RaftProgressResponse{
			Enabled:       st.Enabled,
			State:         st.State,
			LeaderID:      st.LeaderID,
			AppliedIndex:  st.AppliedIndex,
			CommitIndex:   st.CommitIndex,
			LastIndex:     st.LastIndex,
			NodeID:        st.NodeID,
			ClusterID:     st.ClusterID,
			AdvertiseAddr: st.AdvertiseAddr,
		},
	})
}

// CheckReachableRequest is the body of POST /setup/check-reachable.
type CheckReachableRequest struct {
	Addr string `json:"addr" binding:"required"`
}

// CheckReachable opens a quick TCP probe to addr from THIS server and
// reports whether it succeeds. Used by the join wizard's "Waiting for
// snapshot..." state to tell the operator whether the advertise
// address they registered with the leader is actually reachable.
//
// If the joining node can't reach its own advertise address, neither
// can the leader — typical "Raft port not mapped in docker-compose"
// failure mode.
//
// POST /api/v1/setup/check-reachable
func (h *Handler) CheckReachable(c *gin.Context) {
	var req CheckReachableRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": "validation_error", "error": "Invalid request data", "detail": err.Error()})
		return
	}
	addr := strings.TrimSpace(req.Addr)
	if addr == "" {
		c.JSON(http.StatusBadRequest, gin.H{"code": "validation_error", "error": "addr is required"})
		return
	}
	dialer := net.Dialer{Timeout: 3 * time.Second}
	conn, err := dialer.DialContext(c.Request.Context(), "tcp", addr)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"data": gin.H{
				"reachable": false,
				"addr":      addr,
				"error":     err.Error(),
			},
		})
		return
	}
	_ = conn.Close()
	c.JSON(http.StatusOK, gin.H{
		"data": gin.H{"reachable": true, "addr": addr},
	})
}

// AdvertiseCandidate is one TCP-probed advertise-URL option.
type AdvertiseCandidate struct {
	URL       string `json:"url"`
	Reachable bool   `json:"reachable"`
	Error     string `json:"error,omitempty"`
}

// AdvertiseHintsResponse pre-fills the cluster-sync forms: this machine's
// detected LAN IP, the derived Raft advertise address, and advertise-URL
// candidates probed (TCP) from the server's perspective.
type AdvertiseHintsResponse struct {
	IPv4       string               `json:"ipv4"`
	RaftAddr   string               `json:"raft_addr"`
	Candidates []AdvertiseCandidate `json:"candidates"`
}

// AdvertiseHints suggests advertise values for the cluster-sync forms.
// ?origin=<browser origin> adds the URL the operator's browser already uses
// as a candidate. The Raft port itself is NOT probed — nothing listens on it
// until the cluster starts; a reachable HTTP candidate on the same IP is the
// signal that the address is exposed at all.
//
// @Summary     Advertise-address suggestions for cluster forms
// @Tags        cluster
// @Produce     json
// @Param       origin query string false "Browser origin to probe as an advertise-URL candidate"
// @Success     200 {object} map[string]interface{}
// @Security    BearerAuth
// @Router      /cluster/advertise-hints [get]
func (h *Handler) AdvertiseHints(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 8*time.Second)
	defer cancel()
	hints := DetectMachineHints(ctx)

	httpPort := "8080"
	if _, p, err := net.SplitHostPort(h.addr); err == nil && p != "" {
		httpPort = p
	}

	var candidates []string
	if origin := strings.TrimSpace(c.Query("origin")); origin != "" {
		if u, err := url.Parse(origin); err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" {
			candidates = append(candidates, u.Scheme+"://"+u.Host)
		}
	}
	if hints.SuggestedIPv4 != "" {
		lan := "http://" + net.JoinHostPort(hints.SuggestedIPv4, httpPort)
		dup := false
		for _, u := range candidates {
			if u == lan {
				dup = true
			}
		}
		if !dup {
			candidates = append(candidates, lan)
		}
	}

	out := AdvertiseHintsResponse{IPv4: hints.SuggestedIPv4, Candidates: make([]AdvertiseCandidate, len(candidates))}
	if hints.SuggestedIPv4 != "" {
		out.RaftAddr = net.JoinHostPort(hints.SuggestedIPv4, defaultRaftPort())
	}

	var wg sync.WaitGroup
	for i, raw := range candidates {
		wg.Add(1)
		go func(i int, raw string) {
			defer wg.Done()
			cand := AdvertiseCandidate{URL: raw}
			u, err := url.Parse(raw)
			if err != nil {
				cand.Error = err.Error()
				out.Candidates[i] = cand
				return
			}
			port := u.Port()
			if port == "" {
				if u.Scheme == "https" {
					port = "443"
				} else {
					port = "80"
				}
			}
			d := net.Dialer{Timeout: 3 * time.Second}
			conn, derr := d.DialContext(ctx, "tcp", net.JoinHostPort(u.Hostname(), port))
			if derr != nil {
				cand.Error = derr.Error()
			} else {
				_ = conn.Close()
				cand.Reachable = true
			}
			out.Candidates[i] = cand
		}(i, raw)
	}
	wg.Wait()
	c.JSON(http.StatusOK, gin.H{"data": out})
}

// defaultRaftPort is the Raft port this deployment publishes: the installer
// writes NODE_STATS_RAFT_PORT into compose (auto-picking a free one when 7000
// is taken on the host) and the compose passes it into the container, so the
// wizard's bind/advertise defaults must follow it.
func defaultRaftPort() string {
	if p := strings.TrimSpace(os.Getenv("NODE_STATS_RAFT_PORT")); p != "" {
		return p
	}
	return "7000"
}

// deriveJoinerHTTPURL builds a default http://host:port URL from the
// Raft advertise address (which carries the externally-reachable host)
// and the local HTTP ADDR (which carries the port we listen on). Used
// when the wizard's Advertise public URL is empty.
func deriveJoinerHTTPURL(raftAdvertise, httpAddr string) string {
	host := ""
	if h, _, err := net.SplitHostPort(raftAdvertise); err == nil && h != "" {
		host = h
	}
	if host == "" {
		return ""
	}
	port := ""
	if _, p, err := net.SplitHostPort(httpAddr); err == nil && p != "" {
		port = p
	}
	if port == "" {
		port = "8080"
	}
	return "http://" + net.JoinHostPort(host, port)
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
