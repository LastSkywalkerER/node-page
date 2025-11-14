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
	configWriter *setupapp.ConfigWriter
	userService  userservice.UserService
}

// NewSetupHandler creates a new setup handler
func NewSetupHandler(configWriter *setupapp.ConfigWriter, userService userservice.UserService) *SetupHandler {
	return &SetupHandler{
		configWriter: configWriter,
		userService:  userService,
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

// Status checks if setup is needed (no users exist)
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

// CompleteSetup completes the setup process
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

	// Set defaults for optional fields
	if req.Config.Addr == "" {
		req.Config.Addr = ":8080"
	}
	if req.Config.GinMode == "" {
		req.Config.GinMode = "release"
	}
	if req.Config.Debug == "" {
		req.Config.Debug = "false"
	}
	if req.Config.DBType == "" {
		req.Config.DBType = "sqlite"
	}
	if req.Config.DBDSN == "" {
		req.Config.DBDSN = "stats.db"
	}

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
	user, err := h.userService.Register(ctx, req.AdminEmail, req.AdminPassword)
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

	// Success - user created
	_ = user // user is created but we don't need to return it

	c.JSON(http.StatusOK, gin.H{
		"data": CompleteSetupResponse{
			Message: "Setup completed successfully. Please restart the server for changes to take effect.",
		},
	})
}

