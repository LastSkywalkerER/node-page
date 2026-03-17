 // Package config provides application configuration management.
 // This package handles loading configuration from environment variables and .env files.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

 // DatabaseType represents the type of database to use.
type DatabaseType string

const (
	DatabaseTypeSQLite   DatabaseType = "sqlite"
	DatabaseTypePostgres DatabaseType = "postgres"
)

 // DatabaseConfig holds configuration for database connection.
type DatabaseConfig struct {
	Type DatabaseType // Database type: "sqlite"
	DSN  string       // Data Source Name: file path for SQLite database
}

 // Config holds all application configuration loaded from environment variables.
type Config struct {
	// Server configuration
	Addr    string // HTTP server listening address
	GinMode string // Gin framework mode: "debug" or "release"
	Debug   bool   // Enable debug logging

	// Database configuration
	Database DatabaseConfig

	// Authentication configuration
	JWTSecret     string // Secret key for JWT access tokens
	RefreshSecret string // Secret key for JWT refresh tokens

	// Cookie configuration
	CookieSecure bool // COOKIE_SECURE: set Secure flag on cookies (true in production with TLS)

	// CORS configuration
	AllowOrigin string // ALLOW_ORIGIN: allowed CORS origin, default "*"

	// Data retention
	RetentionDays int // METRICS_RETENTION_DAYS: how long to keep historical metrics, default 30
}

 // Load loads application configuration from environment variables.
 // It first attempts to load a .env file if it exists, then reads all configuration
 // from environment variables with appropriate defaults.
func Load() (*Config, error) {
	// Load .env file if it exists (ignore error if file doesn't exist)
	_ = godotenv.Load()

	config := &Config{}

	// Server configuration
	config.Addr = getEnv("ADDR", ":8080")
	config.GinMode = getEnv("GIN_MODE", "release")

	debugEnv := strings.ToLower(getEnv("DEBUG", "false"))
	config.Debug = debugEnv == "true" || debugEnv == "1"

	// Database configuration
	dbConfig, err := loadDatabaseConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to load database configuration: %w", err)
	}
	config.Database = *dbConfig

	// Authentication configuration
	config.JWTSecret = os.Getenv("JWT_SECRET")
	config.RefreshSecret = os.Getenv("REFRESH_SECRET")

	// Cookie configuration
	cookieSecureEnv := strings.ToLower(getEnv("COOKIE_SECURE", "false"))
	config.CookieSecure = cookieSecureEnv == "true" || cookieSecureEnv == "1"

	// CORS configuration
	config.AllowOrigin = getEnv("ALLOW_ORIGIN", "*")

	// Data retention
	retentionStr := getEnv("METRICS_RETENTION_DAYS", "30")
	retentionDays, err := strconv.Atoi(retentionStr)
	if err != nil || retentionDays <= 0 {
		retentionDays = 30
	}
	config.RetentionDays = retentionDays

	// Validate required configuration
	if err := config.validate(); err != nil {
		return nil, err
	}

	return config, nil
}

 // loadDatabaseConfig loads database configuration from environment variables.
func loadDatabaseConfig() (*DatabaseConfig, error) {
	config := &DatabaseConfig{}

	// Determine database type from environment variable (default: sqlite)
	dbType := getEnv("DB_TYPE", "sqlite")
	config.Type = DatabaseType(dbType)

	// Validate database type
	switch config.Type {
	case DatabaseTypeSQLite, DatabaseTypePostgres:
		// supported
	default:
		return nil, fmt.Errorf("unsupported database type: %s (supported: sqlite, postgres)", dbType)
	}

	// For SQLite: use DSN as file path
	config.DSN = getEnv("DB_DSN", "stats.db")
	return config, nil
}

 // validate validates that all required configuration values are present.
func (c *Config) validate() error {
	if c.JWTSecret == "" {
		return fmt.Errorf("JWT_SECRET environment variable is required")
	}
	if c.RefreshSecret == "" {
		return fmt.Errorf("REFRESH_SECRET environment variable is required")
	}
	return nil
}

 // MaskDSN masks sensitive information in a database connection string for logging.
 // This function replaces passwords in DSN strings with asterisks to prevent logging sensitive data.
func MaskDSN(dsn string) string {
	if dsn == "" {
		return ""
	}
	// Simple masking: replace password=xxx with password=***
	if strings.Contains(dsn, "password=") {
		parts := strings.Split(dsn, " ")
		masked := make([]string, 0, len(parts))
		for _, part := range parts {
			if strings.HasPrefix(part, "password=") {
				masked = append(masked, "password=***")
			} else {
				masked = append(masked, part)
			}
		}
		return strings.Join(masked, " ")
	}
	return dsn
}

 // getEnv gets an environment variable value or returns a default if not set.
func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
