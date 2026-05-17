package setup

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
	JWTSecret         string `json:"jwt_secret"`
	RefreshSecret     string `json:"refresh_secret"`
	Addr              string `json:"addr"`
	GinMode           string `json:"gin_mode"`
	Debug             string `json:"debug"`
	DBType            string `json:"db_type"`
	DBDSN             string `json:"db_dsn"`
	PrometheusEnabled string `json:"prometheus_enabled"`
	PrometheusAuth    string `json:"prometheus_auth"`
	PrometheusToken   string `json:"prometheus_token"`
	// DockerHostMetricsCompat adds HOST_* and NODE_HOST_ALIAS for bind-mounted host root at /host.
	DockerHostMetricsCompat bool `json:"docker_host_metrics_compat"`
	// NodeStatsHostname optional; written as NODE_STATS_HOSTNAME when non-empty.
	NodeStatsHostname string `json:"node_stats_hostname"`
	// NodeStatsIPv4 optional; written as NODE_STATS_IPV4 when non-empty.
	NodeStatsIPv4 string `json:"node_stats_ipv4"`

	// Raft cluster sync (optional — only written when RaftEnabled=="true").
	RaftEnabled            string `json:"raft_enabled"`
	RaftClusterID          string `json:"raft_cluster_id"`
	RaftNodeID             string `json:"raft_node_id"`
	RaftBindAddr           string `json:"raft_bind_addr"`
	RaftAdvertiseAddr      string `json:"raft_advertise_addr"`
	RaftDataDir            string `json:"raft_data_dir"`
	RaftBootstrap          string `json:"raft_bootstrap"`
	RaftAdvertisePublicURL string `json:"raft_advertise_public_url"`

	// Cross-cluster bridge (optional).
	RaftBridgeEnabled      string `json:"raft_bridge_enabled"`
	RaftBridgeSharedSecret string `json:"raft_bridge_shared_secret"`
	RaftBridgeRemoteSeeds  string `json:"raft_bridge_remote_seeds"`
}

// ReadCurrentConfig reads current configuration from .env file or environment variables
func (cw *ConfigWriter) ReadCurrentConfig() (*ConfigValues, error) {
	// Try to load .env file if it exists (ignore error if file doesn't exist)
	_ = godotenv.Load(cw.envPath)

	config := &ConfigValues{
		JWTSecret:              os.Getenv("JWT_SECRET"),
		RefreshSecret:          os.Getenv("REFRESH_SECRET"),
		Addr:                   getEnv("ADDR", ":8080"),
		GinMode:                getEnv("GIN_MODE", "release"),
		Debug:                  getEnv("DEBUG", "false"),
		DBType:                 getEnv("DB_TYPE", "sqlite"),
		DBDSN:                  getEnv("DB_DSN", "stats.db"),
		PrometheusEnabled:      getEnv("PROMETHEUS_ENABLED", "false"),
		PrometheusAuth:         getEnv("PROMETHEUS_AUTH", "false"),
		PrometheusToken:        os.Getenv("PROMETHEUS_TOKEN"),
		NodeStatsHostname:      os.Getenv("NODE_STATS_HOSTNAME"),
		NodeStatsIPv4:          os.Getenv("NODE_STATS_IPV4"),
		RaftEnabled:            os.Getenv("RAFT_ENABLED"),
		RaftClusterID:          os.Getenv("RAFT_CLUSTER_ID"),
		RaftNodeID:             os.Getenv("RAFT_NODE_ID"),
		RaftBindAddr:           os.Getenv("RAFT_BIND_ADDR"),
		RaftAdvertiseAddr:      os.Getenv("RAFT_ADVERTISE_ADDR"),
		RaftDataDir:            os.Getenv("RAFT_DATA_DIR"),
		RaftBootstrap:          os.Getenv("RAFT_BOOTSTRAP"),
		RaftAdvertisePublicURL: os.Getenv("RAFT_ADVERTISE_PUBLIC_URL"),
		RaftBridgeEnabled:      os.Getenv("RAFT_BRIDGE_ENABLED"),
		RaftBridgeSharedSecret: os.Getenv("RAFT_BRIDGE_SHARED_SECRET"),
		RaftBridgeRemoteSeeds:  os.Getenv("RAFT_BRIDGE_REMOTE_SEEDS"),
	}
	if strings.TrimSpace(os.Getenv("HOST_PROC")) == "/host/proc" {
		config.DockerHostMetricsCompat = true
	}

	return config, nil
}

// ApplyRaftDefaults fills any Raft fields the wizard didn't provide
// explicitly when RaftEnabled is "true". Used by /setup/complete and
// /setup/preview-env so the operator sees the auto-generated values.
func ApplyRaftDefaults(cv *ConfigValues) {
	if strings.ToLower(strings.TrimSpace(cv.RaftEnabled)) != "true" {
		return
	}
	if strings.TrimSpace(cv.RaftClusterID) == "" {
		cv.RaftClusterID = "local"
	}
	if strings.TrimSpace(cv.RaftNodeID) == "" {
		// Best-effort: use hostname; fall back to "node-1".
		host := strings.ToLower(strings.TrimSpace(os.Getenv("NODE_STATS_HOSTNAME")))
		if host == "" {
			if h, err := os.Hostname(); err == nil {
				host = strings.ToLower(strings.TrimSpace(h))
			}
		}
		if host == "" {
			host = "node-1"
		}
		cv.RaftNodeID = strings.ReplaceAll(host, " ", "-")
	}
	if strings.TrimSpace(cv.RaftBindAddr) == "" {
		cv.RaftBindAddr = ":7000"
	}
	if strings.TrimSpace(cv.RaftDataDir) == "" {
		cv.RaftDataDir = "./data/raft"
	}
	// AdvertiseAddr left empty falls back to BindAddr at the Raft layer.
}

// ApplySetupDefaults fills empty optional fields the same way as setup completion.
func ApplySetupDefaults(cv *ConfigValues) {
	if cv.Addr == "" {
		cv.Addr = ":8080"
	}
	if cv.GinMode == "" {
		cv.GinMode = "release"
	}
	if cv.Debug == "" {
		cv.Debug = "false"
	}
	if cv.DBType == "" {
		cv.DBType = "sqlite"
	}
	if cv.DBDSN == "" {
		cv.DBDSN = "stats.db"
	}
	if cv.PrometheusEnabled == "" {
		cv.PrometheusEnabled = "false"
	}
	if cv.PrometheusAuth == "" {
		cv.PrometheusAuth = "false"
	}
}

// FormatEnvFile returns the exact .env body that WriteConfigFile would persist (after defaults).
func (cw *ConfigWriter) FormatEnvFile(config *ConfigValues) (string, error) {
	cv := *config
	ApplySetupDefaults(&cv)
	applyDockerHostMetricsCompat(&cv)
	if cv.JWTSecret == "" {
		return "", fmt.Errorf("JWT_SECRET is required")
	}
	if cv.RefreshSecret == "" {
		return "", fmt.Errorf("REFRESH_SECRET is required")
	}
	return buildEnvFileContent(&cv), nil
}

// applyDockerHostMetricsCompat adjusts DSN for typical Docker volume layout when enabled.
func applyDockerHostMetricsCompat(cv *ConfigValues) {
	if !cv.DockerHostMetricsCompat {
		return
	}
	if cv.DBType != "sqlite" {
		return
	}
	if cv.DBDSN == "" || cv.DBDSN == "stats.db" {
		cv.DBDSN = "/app/data/stats.db"
	}
}

func buildEnvFileContent(config *ConfigValues) string {
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
	lines = append(lines, "")

	lines = append(lines, "# Prometheus Configuration")
	lines = append(lines, fmt.Sprintf("PROMETHEUS_ENABLED=%s", escapeValue(config.PrometheusEnabled)))
	lines = append(lines, fmt.Sprintf("PROMETHEUS_AUTH=%s", escapeValue(config.PrometheusAuth)))
	if config.PrometheusToken != "" {
		lines = append(lines, fmt.Sprintf("PROMETHEUS_TOKEN=%s", escapeValue(config.PrometheusToken)))
	}

	if config.DockerHostMetricsCompat {
		lines = append(lines, "")
		lines = append(lines, "# Docker: mount host root read-only at /host and Docker socket for metrics (see deployment docs)")
		lines = append(lines, fmt.Sprintf("HOST_PROC=%s", escapeValue("/host/proc")))
		lines = append(lines, fmt.Sprintf("HOST_SYS=%s", escapeValue("/host/sys")))
		lines = append(lines, fmt.Sprintf("HOST_ETC=%s", escapeValue("/host/etc")))
		lines = append(lines, fmt.Sprintf("HOST_ROOT=%s", escapeValue("/host")))
		lines = append(lines, fmt.Sprintf("NODE_HOST_ALIAS=%s", escapeValue("host.docker.internal")))
	}

	if strings.TrimSpace(config.NodeStatsHostname) != "" || strings.TrimSpace(config.NodeStatsIPv4) != "" {
		lines = append(lines, "")
		lines = append(lines, "# Optional: machine list card (set hostname to show a label; IPv4 overrides auto-detect)")
		if strings.TrimSpace(config.NodeStatsHostname) != "" {
			lines = append(lines, fmt.Sprintf("NODE_STATS_HOSTNAME=%s", escapeValue(strings.TrimSpace(config.NodeStatsHostname))))
		}
		if strings.TrimSpace(config.NodeStatsIPv4) != "" {
			lines = append(lines, fmt.Sprintf("NODE_STATS_IPV4=%s", escapeValue(strings.TrimSpace(config.NodeStatsIPv4))))
		}
	}

	if strings.ToLower(strings.TrimSpace(config.RaftEnabled)) == "true" {
		lines = append(lines, "")
		lines = append(lines, "# Raft cluster sync (configured from setup wizard)")
		lines = append(lines, "RAFT_ENABLED=true")
		if v := strings.TrimSpace(config.RaftClusterID); v != "" {
			lines = append(lines, fmt.Sprintf("RAFT_CLUSTER_ID=%s", escapeValue(v)))
		}
		if v := strings.TrimSpace(config.RaftNodeID); v != "" {
			lines = append(lines, fmt.Sprintf("RAFT_NODE_ID=%s", escapeValue(v)))
		}
		if v := strings.TrimSpace(config.RaftBindAddr); v != "" {
			lines = append(lines, fmt.Sprintf("RAFT_BIND_ADDR=%s", escapeValue(v)))
		}
		if v := strings.TrimSpace(config.RaftAdvertiseAddr); v != "" {
			lines = append(lines, fmt.Sprintf("RAFT_ADVERTISE_ADDR=%s", escapeValue(v)))
		}
		if v := strings.TrimSpace(config.RaftDataDir); v != "" {
			lines = append(lines, fmt.Sprintf("RAFT_DATA_DIR=%s", escapeValue(v)))
		}
		if strings.ToLower(strings.TrimSpace(config.RaftBootstrap)) == "true" {
			lines = append(lines, "RAFT_BOOTSTRAP=true")
		}
		if v := strings.TrimSpace(config.RaftAdvertisePublicURL); v != "" {
			lines = append(lines, fmt.Sprintf("RAFT_ADVERTISE_PUBLIC_URL=%s", escapeValue(v)))
		}
		if strings.ToLower(strings.TrimSpace(config.RaftBridgeEnabled)) == "true" {
			lines = append(lines, "RAFT_BRIDGE_ENABLED=true")
		}
		if v := strings.TrimSpace(config.RaftBridgeSharedSecret); v != "" {
			lines = append(lines, fmt.Sprintf("RAFT_BRIDGE_SHARED_SECRET=%s", escapeValue(v)))
		}
		if v := strings.TrimSpace(config.RaftBridgeRemoteSeeds); v != "" {
			lines = append(lines, fmt.Sprintf("RAFT_BRIDGE_REMOTE_SEEDS=%s", escapeValue(v)))
		}
	}

	return strings.Join(lines, "\n") + "\n"
}

// WriteConfigFile writes configuration values to .env file
func (cw *ConfigWriter) WriteConfigFile(config *ConfigValues) error {
	content, err := cw.FormatEnvFile(config)
	if err != nil {
		return err
	}
	if err := os.WriteFile(cw.envPath, []byte(content), 0600); err != nil {
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
