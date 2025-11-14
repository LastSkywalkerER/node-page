package application

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/joho/godotenv"
)

// ConfigWriter handles reading and writing .env configuration files
type ConfigWriter struct {
	envPath string
}

// NewConfigWriter creates a new config writer instance
func NewConfigWriter() *ConfigWriter {
	// Get current working directory
	wd, err := os.Getwd()
	if err != nil {
		// Fallback to current directory
		wd = "."
	}
	
	envPath := filepath.Join(wd, ".env")
	return &ConfigWriter{
		envPath: envPath,
	}
}

// ConfigValues represents all configuration values
type ConfigValues struct {
	JWTSecret     string `json:"jwt_secret"`
	RefreshSecret string `json:"refresh_secret"`
	Addr          string `json:"addr"`
	GinMode       string `json:"gin_mode"`
	Debug         string `json:"debug"`
	DBType        string `json:"db_type"`
	DBDSN         string `json:"db_dsn"`
}

// ReadCurrentConfig reads current configuration from .env file or environment variables
func (cw *ConfigWriter) ReadCurrentConfig() (*ConfigValues, error) {
	// Try to load .env file if it exists (ignore error if file doesn't exist)
	_ = godotenv.Load(cw.envPath)

	config := &ConfigValues{
		JWTSecret:     os.Getenv("JWT_SECRET"),
		RefreshSecret: os.Getenv("REFRESH_SECRET"),
		Addr:          getEnv("ADDR", ":8080"),
		GinMode:       getEnv("GIN_MODE", "release"),
		Debug:         getEnv("DEBUG", "false"),
		DBType:        getEnv("DB_TYPE", "sqlite"),
		DBDSN:         getEnv("DB_DSN", "stats.db"),
	}

	return config, nil
}

// WriteConfigFile writes configuration values to .env file
func (cw *ConfigWriter) WriteConfigFile(config *ConfigValues) error {
	// Validate required fields
	if config.JWTSecret == "" {
		return fmt.Errorf("JWT_SECRET is required")
	}
	if config.RefreshSecret == "" {
		return fmt.Errorf("REFRESH_SECRET is required")
	}

	// Build .env file content
	var lines []string
	
	lines = append(lines, "# Server Configuration")
	lines = append(lines, fmt.Sprintf("ADDR=%s", escapeValue(config.Addr)))
	lines = append(lines, fmt.Sprintf("GIN_MODE=%s", escapeValue(config.GinMode)))
	lines = append(lines, fmt.Sprintf("DEBUG=%s", escapeValue(config.Debug)))
	lines = append(lines, "")
	
	lines = append(lines, "# Database Configuration")
	lines = append(lines, fmt.Sprintf("DB_TYPE=%s", escapeValue(config.DBType)))
	lines = append(lines, fmt.Sprintf("DB_DSN=%s", escapeValue(config.DBDSN)))
	lines = append(lines, "")
	
	lines = append(lines, "# Authentication Configuration")
	lines = append(lines, fmt.Sprintf("JWT_SECRET=%s", escapeValue(config.JWTSecret)))
	lines = append(lines, fmt.Sprintf("REFRESH_SECRET=%s", escapeValue(config.RefreshSecret)))

	content := strings.Join(lines, "\n") + "\n"

	// Write to file
	err := os.WriteFile(cw.envPath, []byte(content), 0600)
	if err != nil {
		return fmt.Errorf("failed to write .env file: %w", err)
	}

	return nil
}

// GetConfigPath returns the path to the .env file
func (cw *ConfigWriter) GetConfigPath() string {
	return cw.envPath
}

// escapeValue escapes special characters in environment variable values
func escapeValue(value string) string {
	// If value contains spaces, quotes, or special characters, wrap in quotes
	if strings.ContainsAny(value, " \t\n\"'$`\\") {
		// Escape quotes and backslashes
		escaped := strings.ReplaceAll(value, "\\", "\\\\")
		escaped = strings.ReplaceAll(escaped, "\"", "\\\"")
		return fmt.Sprintf("\"%s\"", escaped)
	}
	return value
}

// getEnv gets an environment variable value or returns a default if not set
func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

