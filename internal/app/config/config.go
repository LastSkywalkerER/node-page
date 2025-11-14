/**
 * Package config provides application configuration management.
 * This package handles loading configuration from environment variables and .env files.
 */
package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/joho/godotenv"
)

/**
 * DatabaseType represents the type of database to use.
 */
type DatabaseType string

const (
	DatabaseTypeSQLite DatabaseType = "sqlite"
)

/**
 * DatabaseConfig holds configuration for database connection.
 */
type DatabaseConfig struct {
	Type DatabaseType // Database type: "sqlite"
	DSN  string       // Data Source Name: file path for SQLite database
}

/**
 * Config holds all application configuration loaded from environment variables.
 */
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
}

/**
 * Load loads application configuration from environment variables.
 * It first attempts to load a .env file if it exists, then reads all configuration
 * from environment variables with appropriate defaults.
 *
 * @return *Config The loaded configuration
 * @return error Returns an error if required configuration is missing or invalid
 */
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

	// Validate required configuration
	if err := config.validate(); err != nil {
		return nil, err
	}

	return config, nil
}

/**
 * loadDatabaseConfig loads database configuration from environment variables.
 *
 * @return *DatabaseConfig The database configuration
 * @return error Returns an error if configuration is invalid
 */
func loadDatabaseConfig() (*DatabaseConfig, error) {
	config := &DatabaseConfig{}

	// Determine database type from environment variable (default: sqlite)
	dbType := getEnv("DB_TYPE", "sqlite")
	config.Type = DatabaseType(dbType)

	// Validate database type
	if config.Type != DatabaseTypeSQLite {
		return nil, fmt.Errorf("unsupported database type: %s (supported: sqlite)", dbType)
	}

	// For SQLite: use DSN as file path
	config.DSN = getEnv("DB_DSN", "stats.db")
	return config, nil
}

/**
 * validate validates that all required configuration values are present.
 *
 * @return error Returns an error if required configuration is missing
 */
func (c *Config) validate() error {
	if c.JWTSecret == "" {
		return fmt.Errorf("JWT_SECRET environment variable is required")
	}
	if c.RefreshSecret == "" {
		return fmt.Errorf("REFRESH_SECRET environment variable is required")
	}
	return nil
}

/**
 * MaskDSN masks sensitive information in a database connection string for logging.
 * This function replaces passwords in DSN strings with asterisks to prevent logging sensitive data.
 *
 * @param dsn The database connection string
 * @return string The DSN string with masked password
 */
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

/**
 * getEnv gets an environment variable value or returns a default if not set.
 *
 * @param key The environment variable key
 * @param defaultValue The default value to return if the variable is not set
 * @return string The environment variable value or default
 */
func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
