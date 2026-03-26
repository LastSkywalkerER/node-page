package presentation

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	setupapp "system-stats/internal/modules/setup/application"
	userservice "system-stats/internal/modules/users/application"
)

// SetupHandler handles setup-related HTTP requests
type SetupHandler struct {
	configWriter    *setupapp.ConfigWriter
	userService     userservice.UserService
	onSetupComplete func() // called once after setup finishes; may be nil
}

// NewSetupHandler creates a new setup handler
func NewSetupHandler(configWriter *setupapp.ConfigWriter, userService userservice.UserService, onSetupComplete func()) *SetupHandler {
	return &SetupHandler{
		configWriter:    configWriter,
		userService:     userService,
		onSetupComplete: onSetupComplete,
	}
}

// SetupStatusResponse represents the setup status response
type SetupStatusResponse struct {
	SetupNeeded bool `json:"setup_needed"`
}

// ConfigResponse represents the current configuration response
type ConfigResponse struct {
	Config *setupapp.ConfigValues `json:"config"`
}

// CompleteSetupRequest represents the complete setup request
type CompleteSetupRequest struct {
	Config     *setupapp.ConfigValues `json:"config" binding:"required"`
	AdminEmail string                 `json:"admin_email" binding:"required,email"`
	AdminPassword string               `json:"admin_password" binding:"required,min=8"`
}

// CompleteSetupResponse represents the complete setup response
type CompleteSetupResponse struct {
	Message string `json:"message"`
}

// PreviewEnvRequest is the body for POST /setup/preview-env.
type PreviewEnvRequest struct {
	Config *setupapp.ConfigValues `json:"config" binding:"required"`
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
func (h *SetupHandler) Status(c *gin.Context) {
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
	
	c.JSON(http.StatusOK, gin.H{
		"data": SetupStatusResponse{
			SetupNeeded: setupNeeded,
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
func (h *SetupHandler) GetConfig(c *gin.Context) {
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
func (h *SetupHandler) PreviewEnv(c *gin.Context) {
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
			"code":   "validation_error",
			"error":  err.Error(),
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
func (h *SetupHandler) CompleteSetup(c *gin.Context) {
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

	setupapp.ApplySetupDefaults(req.Config)

	// Write configuration to .env file
	if err := h.configWriter.WriteConfigFile(req.Config); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code":  "config_write_error",
			"error": "Failed to write configuration file",
			"detail": err.Error(),
		})
		return
	}

	// Create first admin user
	user, err := h.userService.Register(ctx, req.AdminEmail, req.AdminPassword, nil)
	if err != nil {
		// Try to clean up .env file if user creation fails
		// (but don't fail if cleanup fails)
		_ = h.configWriter.WriteConfigFile(&setupapp.ConfigValues{
			JWTSecret:     "",
			RefreshSecret: "",
			Addr:          req.Config.Addr,
			GinMode:       req.Config.GinMode,
			Debug:         req.Config.Debug,
			DBType:        req.Config.DBType,
			DBDSN:         req.Config.DBDSN,
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

