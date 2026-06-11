package setup

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/joho/godotenv"

	appconfig "system-stats/internal/app/config"
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
	// Structured Postgres connection fields. When DBType=="postgres" and DBHost
	// is set, AssembleDSN builds the GORM keyword DSN from these (so the wizard
	// collects + validates components); otherwise the raw DBDSN is used as-is.
	// These are inputs only — they are not written to .env individually; the
	// resolved connection string lands in DB_DSN.
	DBHost     string `json:"db_host"`
	DBPort     string `json:"db_port"`
	DBName     string `json:"db_name"`
	DBUser     string `json:"db_user"`
	DBPassword string `json:"db_password"`
	DBSSLMode  string `json:"db_sslmode"`
	// DBManaged is true when node-stats should provision its own Postgres
	// container (compose service "db") rather than connect to an external server.
	// Only meaningful in Docker deployments (the controller injects the service).
	DBManaged         bool   `json:"db_managed"`
	PrometheusEnabled string `json:"prometheus_enabled"`
	PrometheusAuth    string `json:"prometheus_auth"`
	PrometheusToken   string `json:"prometheus_token"`
	// AutoUpdate ("true"/"false"): when on, the app checks GitHub Releases and
	// applies updates (docker → controller pull+recreate; native → self-replace).
	AutoUpdate string `json:"auto_update"`
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
	RaftBridgeMode         string `json:"raft_bridge_mode"`
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
		AutoUpdate:             getEnv("AUTO_UPDATE", "false"),
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
		RaftBridgeMode:         os.Getenv("RAFT_BRIDGE_MODE"),
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
		cv.RaftBindAddr = ":" + defaultRaftPort()
	}
	if strings.TrimSpace(cv.RaftDataDir) == "" {
		cv.RaftDataDir = appconfig.DefaultRaftDataDir()
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
	if strings.EqualFold(cv.DBType, "postgres") {
		if cv.DBPort == "" {
			cv.DBPort = "5432"
		}
		if cv.DBSSLMode == "" {
			cv.DBSSLMode = "disable"
		}
		if cv.DBName == "" {
			cv.DBName = "node_stats"
		}
	}
	if cv.DBDSN == "" && !strings.EqualFold(cv.DBType, "postgres") {
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
	// Resolve the connection string the app will use (DB_DSN). For postgres
	// with structured fields this builds the GORM keyword form; otherwise the
	// raw DSN / sqlite path passes through unchanged.
	cv.DBDSN = AssembleDSN(&cv)
	if cv.JWTSecret == "" {
		return "", fmt.Errorf("JWT_SECRET is required")
	}
	if cv.RefreshSecret == "" {
		return "", fmt.Errorf("REFRESH_SECRET is required")
	}
	return buildEnvFileContent(&cv), nil
}

// AssembleDSN returns the connection string to persist as DB_DSN. For
// DBType=="postgres" with a structured host present it builds the GORM keyword
// form ("host=… user=… password=… dbname=… port=… sslmode=…", matching
// database.initPostgres). For sqlite, or postgres with a hand-entered DSN
// (DBHost empty), it returns the raw DBDSN unchanged.
func AssembleDSN(cv *ConfigValues) string {
	if !strings.EqualFold(strings.TrimSpace(cv.DBType), "postgres") {
		return cv.DBDSN
	}
	if strings.TrimSpace(cv.DBHost) == "" {
		return cv.DBDSN // hand-entered DSN provided directly
	}
	port := strings.TrimSpace(cv.DBPort)
	if port == "" {
		port = "5432"
	}
	ssl := strings.TrimSpace(cv.DBSSLMode)
	if ssl == "" {
		ssl = "disable"
	}
	name := strings.TrimSpace(cv.DBName)
	if name == "" {
		name = "node_stats"
	}
	return fmt.Sprintf("host=%s user=%s password=%s dbname=%s port=%s sslmode=%s",
		strings.TrimSpace(cv.DBHost), strings.TrimSpace(cv.DBUser), cv.DBPassword, name, port, ssl)
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

	if strings.EqualFold(strings.TrimSpace(config.AutoUpdate), "true") {
		lines = append(lines, "")
		lines = append(lines, "# Auto-update: check GitHub Releases and apply (docker → controller pull+recreate)")
		lines = append(lines, "AUTO_UPDATE=true")
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
// using single quotes when the value contains any character godotenv would
// otherwise interpret. Single quotes disable variable expansion ($var),
// command substitution ($(...) / `...`) and backslash escapes, so the
// stored value round-trips exactly — critical for secrets that may
// legitimately contain '$' or '@' (e.g. generated JWT secrets).
func escapeValue(value string) string {
	if !strings.ContainsAny(value, " \t\n\"'$`\\#=") {
		return value
	}
	// Escape only the single quote — everything else inside '...' is literal.
	escaped := strings.ReplaceAll(value, "'", `'\''`)
	return "'" + escaped + "'"
}

// getEnv gets an environment variable value or returns a default if not set
func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
