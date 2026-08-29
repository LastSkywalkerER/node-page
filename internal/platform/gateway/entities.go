// Package gateway implements the cluster "gateway" (ingress) feature: a
// Raft-replicated, provider-neutral table of HTTP routes (domain → target
// host:port) that ONE chosen node materialises as a Traefik file-provider
// dynamic config — either into a Traefik container node-stats deploys itself
// through the controller sidecar ("managed" mode) or into the dynamic dir of an
// operator-run Traefik ("external" mode).
//
// Everything is declarative: routes + the gateway config live in the DB
// (replicated when Raft is on); the gateway node's Materializer reconciles the
// on-disk YAML (and, in managed mode, the controller's desired-state) from them.
// Nothing is written straight from an HTTP handler, so the gateway can move to
// another node just by changing the config, and a restart re-renders from state.
package gateway

import (
	"time"

	"gorm.io/gorm"
)

// Gateway modes.
const (
	// ModeManaged: node-stats asks its controller sidecar to run a Traefik
	// container on the gateway node (Docker installs only).
	ModeManaged = "managed"
	// ModeExternal: the operator already runs Traefik; node-stats only writes
	// the dynamic file into its (bind-mounted, writable) file-provider dir.
	ModeExternal = "external"
)

// ConfigKey is the cluster_config key holding the JSON-encoded Config.
const ConfigKey = "gateway_config"

// DynamicFileName is the single file node-stats owns inside the dynamic dir.
// Everything else in that dir is the operator's and is never touched.
const DynamicFileName = "node-stats.yml"

// CertResolverName is the ACME resolver id referenced by TLS routers.
const CertResolverName = "le"

// Target schemes.
const (
	SchemeHTTP  = "http"
	SchemeHTTPS = "https"
)

// Config is the cluster-wide gateway configuration (stored as one JSON value
// in cluster_config so it replicates with CmdConfigSet).
type Config struct {
	Enabled bool `json:"enabled"`
	// Mode is ModeManaged or ModeExternal.
	Mode string `json:"mode"`
	// NodeMAC identifies the gateway node (hosts.mac_address — the cluster-
	// stable identity; local host ids differ per node).
	NodeMAC string `json:"node_mac"`
	// NodeName is a display hint (the host's name when the MAC was picked).
	NodeName string `json:"node_name,omitempty"`
	// DynamicDir (external mode) is the Traefik file-provider directory as
	// seen from the node-stats process on the gateway node.
	DynamicDir string `json:"dynamic_dir,omitempty"`
	// HTTPPort / HTTPSPort (managed mode) are the host ports published for the
	// Traefik entrypoints. Zero → 80 / 443.
	HTTPPort  int `json:"http_port,omitempty"`
	HTTPSPort int `json:"https_port,omitempty"`
	// ACMEEnabled turns on Let's Encrypt (HTTP-01) for routers with TLS.
	ACMEEnabled bool   `json:"acme_enabled"`
	ACMEEmail   string `json:"acme_email,omitempty"`
	// ACMEStaging points the resolver at the LE staging CA (rate-limit-safe
	// while testing; issues untrusted certs).
	ACMEStaging bool `json:"acme_staging,omitempty"`
}

// Route is one published hostname. Provider-neutral: the Traefik renderer is
// one consumer; other renderers (Caddy, NPM) can be added without a schema
// change. Replicated by RouteID (never by the local autoincrement id).
type Route struct {
	ID uint `json:"id" gorm:"primaryKey"`
	// RouteID is the cluster-stable identity (short random hex).
	RouteID string `json:"route_id" gorm:"uniqueIndex;size:32;not null"`
	Name    string `json:"name" gorm:"size:128"`

	// Domain is the public hostname (lower-case FQDN). PathPrefix optionally
	// narrows the match (e.g. "/grafana").
	Domain     string `json:"domain" gorm:"index;size:253;not null"`
	PathPrefix string `json:"path_prefix,omitempty" gorm:"size:255"`

	// Target is where the gateway forwards to. TargetHost is an IP/hostname
	// reachable FROM the gateway node (published host port for containers on
	// other machines). TargetHostMAC optionally links the target to a hosts
	// row for display / re-resolution; TargetLabel is a display hint
	// ("nextcloud · app").
	TargetScheme  string `json:"target_scheme" gorm:"size:8;not null"`
	TargetHost    string `json:"target_host" gorm:"size:253;not null"`
	TargetPort    int    `json:"target_port" gorm:"not null"`
	TargetHostMAC string `json:"target_host_mac,omitempty" gorm:"index;size:64"`
	TargetLabel   string `json:"target_label,omitempty" gorm:"size:255"`
	// TargetInsecureSkipVerify disables upstream cert verification for https
	// targets (self-signed admin UIs).
	TargetInsecureSkipVerify bool `json:"target_insecure_skip_verify"`

	// TLS terminates HTTPS on the gateway (cert via ACME when enabled,
	// otherwise Traefik's default self-signed). Plain-HTTP requests are
	// redirected to HTTPS.
	TLS bool `json:"tls"`

	// BasicAuthUsers holds htpasswd-style "user:$2a$…" lines (newline-
	// separated, bcrypt). Never returned by the API — only the user names.
	BasicAuthUsers string `json:"-" gorm:"type:text"`
	// IPAllowList is a comma-separated CIDR list; empty = allow all.
	IPAllowList string `json:"ip_allow_list,omitempty" gorm:"type:text"`

	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// TableName pins the GORM table name.
func (Route) TableName() string { return "gateway_routes" }

// AutoMigrate creates the gateway tables (called from the central migrations).
func AutoMigrate(db *gorm.DB) error {
	return db.AutoMigrate(&Route{})
}

// Protected reports whether the route has any access-control middleware.
func (r Route) Protected() bool {
	return r.BasicAuthUsers != "" || r.IPAllowList != ""
}

// PublicURL is the browser-facing URL of the route.
func (r Route) PublicURL(cfg Config) string {
	scheme := SchemeHTTP
	port := ""
	if r.TLS {
		scheme = SchemeHTTPS
		if cfg.Mode == ModeManaged && cfg.HTTPSPort != 0 && cfg.HTTPSPort != 443 {
			port = ":" + itoa(cfg.HTTPSPort)
		}
	} else if cfg.Mode == ModeManaged && cfg.HTTPPort != 0 && cfg.HTTPPort != 80 {
		port = ":" + itoa(cfg.HTTPPort)
	}
	return scheme + "://" + r.Domain + port + r.PathPrefix
}
