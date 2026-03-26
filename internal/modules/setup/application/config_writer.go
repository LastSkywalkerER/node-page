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
}

// ReadCurrentConfig reads current configuration from .env file or environment variables
func (cw *ConfigWriter) ReadCurrentConfig() (*ConfigValues, error) {
	// Try to load .env file if it exists (ignore error if file doesn't exist)
	_ = godotenv.Load(cw.envPath)

	config := &ConfigValues{
		JWTSecret:         os.Getenv("JWT_SECRET"),
		RefreshSecret:     os.Getenv("REFRESH_SECRET"),
		Addr:              getEnv("ADDR", ":8080"),
		GinMode:           getEnv("GIN_MODE", "release"),
		Debug:             getEnv("DEBUG", "false"),
		DBType:            getEnv("DB_TYPE", "sqlite"),
		DBDSN:             getEnv("DB_DSN", "stats.db"),
		PrometheusEnabled: getEnv("PROMETHEUS_ENABLED", "false"),
		PrometheusAuth:    getEnv("PROMETHEUS_AUTH", "false"),
		PrometheusToken:   os.Getenv("PROMETHEUS_TOKEN"),
		NodeStatsHostname: os.Getenv("NODE_STATS_HOSTNAME"),
		NodeStatsIPv4:     os.Getenv("NODE_STATS_IPV4"),
	}
	if strings.TrimSpace(os.Getenv("HOST_PROC")) == "/host/proc" {
		config.DockerHostMetricsCompat = true
	}

	return config, nil
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

