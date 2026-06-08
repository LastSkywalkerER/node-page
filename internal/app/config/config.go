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

	// Observability
	PrometheusEnabled bool   // PROMETHEUS_ENABLED: expose /metrics endpoint, default false
	PrometheusAuth    bool   // PROMETHEUS_AUTH: require Bearer token for /metrics, default false
	PrometheusToken   string // PROMETHEUS_TOKEN: bearer token value when auth is enabled

	// TrustedProxies is the comma-separated list of CIDRs (or IPs) of reverse proxies whose
	// X-Forwarded-* headers may be trusted. Empty (default) means trust no proxy — c.ClientIP()
	// returns the direct peer address and X-Forwarded-Host/Proto are ignored.
	TrustedProxies []string

	// TraefikDynamicDirs are filesystem paths to Traefik file-provider dynamic
	// config directories (TRAEFIK_DYNAMIC_DIR, colon-separated) scanned to
	// derive per-service public URLs for the Applications view. Empty falls
	// back to a list of well-known defaults probed at read time. The paths
	// must be reachable from this process (bind-mounted in Docker deployments).
	TraefikDynamicDirs []string

	// Raft holds optional consensus / cross-cluster sync configuration.
	Raft RaftConfig
}

// RaftConfig holds settings for the HashiCorp Raft layer and the cross-cluster bridge.
// All fields are no-ops when Enabled is false.
type RaftConfig struct {
	Enabled       bool   // RAFT_ENABLED
	ClusterID     string // RAFT_CLUSTER_ID — "local" or "public"
	NodeID        string // RAFT_NODE_ID — unique within the cluster
	BindAddr      string // RAFT_BIND_ADDR — transport listen, default ":7000"
	AdvertiseAddr string // RAFT_ADVERTISE_ADDR — address peers should dial
	DataDir       string // RAFT_DATA_DIR — log + snapshot store, default "./data/raft"
	Bootstrap     bool   // RAFT_BOOTSTRAP — true only on the first node of a cluster
	Peers         []RaftPeer
	AdvertiseURL  string // RAFT_ADVERTISE_PUBLIC_URL — published into the URL catalog

	Bridge RaftBridgeConfig
}

// RaftPeer is a peer Raft voter to seed the cluster with on startup.
type RaftPeer struct {
	ID   string
	Addr string
}

// RaftBridgeConfig configures the asynchronous cross-cluster replication bridge.
type RaftBridgeConfig struct {
	Enabled      bool     // RAFT_BRIDGE_ENABLED
	RemoteSeeds  []string // RAFT_BRIDGE_REMOTE_SEEDS — initial peer cluster URLs
	SharedSecret string   // RAFT_BRIDGE_SHARED_SECRET — HMAC key
}

// Load loads application configuration from environment variables.
// It first attempts to load a .env file if it exists, then reads all configuration
// from environment variables with appropriate defaults.
func Load() (*Config, error) {
	// Load .env file if it exists (ignore error if file doesn't exist)
	_ = godotenv.Load()
	applyHostProcFromBindMount()

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

	if raw := strings.TrimSpace(getEnv("TRAEFIK_DYNAMIC_DIR", "")); raw != "" {
		for _, p := range strings.Split(raw, ":") {
			if p = strings.TrimSpace(p); p != "" {
				config.TraefikDynamicDirs = append(config.TraefikDynamicDirs, p)
			}
		}
	}

	// Data retention
	retentionStr := getEnv("METRICS_RETENTION_DAYS", "30")
	retentionDays, err := strconv.Atoi(retentionStr)
	if err != nil || retentionDays <= 0 {
		retentionDays = 30
	}
	config.RetentionDays = retentionDays

	// Observability
	prometheusEnv := strings.ToLower(getEnv("PROMETHEUS_ENABLED", "false"))
	config.PrometheusEnabled = prometheusEnv == "true" || prometheusEnv == "1"
	prometheusAuthEnv := strings.ToLower(getEnv("PROMETHEUS_AUTH", "false"))
	config.PrometheusAuth = prometheusAuthEnv == "true" || prometheusAuthEnv == "1"
	config.PrometheusToken = os.Getenv("PROMETHEUS_TOKEN")

	// TrustedProxies: comma-separated CIDRs / IPs. Empty disables proxy header trust.
	if tp := strings.TrimSpace(getEnv("TRUSTED_PROXIES", "")); tp != "" {
		for _, p := range strings.Split(tp, ",") {
			if v := strings.TrimSpace(p); v != "" {
				config.TrustedProxies = append(config.TrustedProxies, v)
			}
		}
	}

	config.Raft = loadRaftConfig()

	return config, nil
}

// loadRaftConfig reads RAFT_* env vars. When RAFT_ENABLED is unset/false, the
// other fields are still parsed but the layer stays disabled in the DI container.
func loadRaftConfig() RaftConfig {
	enabled := strings.ToLower(getEnv("RAFT_ENABLED", "false"))
	cfg := RaftConfig{
		Enabled:       enabled == "true" || enabled == "1",
		ClusterID:     strings.TrimSpace(getEnv("RAFT_CLUSTER_ID", "")),
		NodeID:        strings.TrimSpace(getEnv("RAFT_NODE_ID", "")),
		BindAddr:      strings.TrimSpace(getEnv("RAFT_BIND_ADDR", ":7000")),
		AdvertiseAddr: strings.TrimSpace(getEnv("RAFT_ADVERTISE_ADDR", "")),
		DataDir:       strings.TrimSpace(getEnv("RAFT_DATA_DIR", "./data/raft")),
		AdvertiseURL:  strings.TrimSuffix(strings.TrimSpace(getEnv("RAFT_ADVERTISE_PUBLIC_URL", "")), "/"),
	}
	bootstrap := strings.ToLower(getEnv("RAFT_BOOTSTRAP", "false"))
	cfg.Bootstrap = bootstrap == "true" || bootstrap == "1"

	// RAFT_PEERS format: "id1@host1:port,id2@host2:port"
	if raw := strings.TrimSpace(getEnv("RAFT_PEERS", "")); raw != "" {
		for _, p := range strings.Split(raw, ",") {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			id, addr, ok := strings.Cut(p, "@")
			if !ok || strings.TrimSpace(id) == "" || strings.TrimSpace(addr) == "" {
				continue
			}
			cfg.Peers = append(cfg.Peers, RaftPeer{ID: strings.TrimSpace(id), Addr: strings.TrimSpace(addr)})
		}
	}

	bridgeEnabled := strings.ToLower(getEnv("RAFT_BRIDGE_ENABLED", "false"))
	cfg.Bridge = RaftBridgeConfig{
		Enabled:      bridgeEnabled == "true" || bridgeEnabled == "1",
		SharedSecret: getEnv("RAFT_BRIDGE_SHARED_SECRET", ""),
	}
	if raw := strings.TrimSpace(getEnv("RAFT_BRIDGE_REMOTE_SEEDS", "")); raw != "" {
		for _, s := range strings.Split(raw, ",") {
			if v := strings.TrimSuffix(strings.TrimSpace(s), "/"); v != "" {
				cfg.Bridge.RemoteSeeds = append(cfg.Bridge.RemoteSeeds, v)
			}
		}
	}

	return cfg
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

// RequireAuthSecrets returns an error if JWT signing secrets are missing.
// Call this after DB checks when at least one user exists (post-setup runtime).
func (c *Config) RequireAuthSecrets() error {
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
