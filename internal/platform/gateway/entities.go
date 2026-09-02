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
	"strings"
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

// Route modes.
const (
	// ModeHTTP (default): the gateway terminates TLS and reverse-proxies HTTP to
	// the target.
	RouteModeHTTP = "http"
	// RouteModePassthrough delegates a hostname (typically a wildcard) to
	// ANOTHER reverse proxy: :443 traffic is forwarded as raw TLS by SNI
	// (passthrough — that proxy terminates TLS and runs its own ACME) and :80
	// traffic is proxied as HTTP to its http port (so its HTTP-01 challenges and
	// redirects work).
	RouteModePassthrough = "passthrough"
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
	// NodeSystemID is the picked host's stable machine identity
	// (hosts.system_host_id, falling back to hardware_uuid). A Docker
	// container's NIC MAC changes on every recreate, so the gateway node also
	// recognises itself by this id — otherwise a controller recreate would make
	// the node "lose" the gateway role and tear Traefik down.
	NodeSystemID string `json:"node_system_id,omitempty"`
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
	// RequestReadTimeoutSeconds (managed mode) is Traefik's entrypoint
	// respondingTimeouts.readTimeout: how long a client may take to send a
	// whole request INCLUDING its body, i.e. the upload ceiling. Traefik v3
	// defaults to 60s, which kills any upload slower than a minute. It is an
	// entrypoint (listener) setting — there is no per-route knob — so it lives
	// here; routes tighten with their own limits (RouteLimits). 0 → the
	// node-stats default (DefaultRequestReadTimeoutSeconds), -1 → unlimited.
	RequestReadTimeoutSeconds int `json:"request_read_timeout_seconds,omitempty"`
	// (The Let's Encrypt staging CA is a developer-only knob —
	// NODE_STATS_ACME_STAGING=1 on the gateway node — not part of the config.)
}

// Route is one published hostname. Provider-neutral: the Traefik renderer is
// one consumer; other renderers (Caddy, NPM) can be added without a schema
// change. Replicated by RouteID (never by the local autoincrement id).
type Route struct {
	ID uint `json:"id" gorm:"primaryKey"`
	// RouteID is the cluster-stable identity (short random hex).
	RouteID string `json:"route_id" gorm:"uniqueIndex;size:32;not null"`
	Name    string `json:"name" gorm:"size:128"`

	// Mode is RouteModeHTTP (default) or RouteModePassthrough.
	Mode string `json:"mode" gorm:"size:16"`

	// Domain is the public hostname (lower-case FQDN). A leading "*." makes it
	// a wildcard (all direct subdomains; matched with HostRegexp/HostSNIRegexp).
	// PathPrefix optionally narrows the match (e.g. "/grafana"; http mode only).
	Domain     string `json:"domain" gorm:"index;size:253;not null"`
	PathPrefix string `json:"path_prefix,omitempty" gorm:"size:255"`

	// Target is where the gateway forwards to. TargetHost is an IP/hostname
	// reachable FROM the gateway node (published host port for containers on
	// other machines). TargetHostMAC optionally links the target to a hosts
	// row for display / re-resolution; TargetLabel is a display hint
	// ("nextcloud · app").
	TargetScheme string `json:"target_scheme" gorm:"size:8;not null"`
	TargetHost   string `json:"target_host" gorm:"size:253;not null"`
	TargetPort   int    `json:"target_port" gorm:"not null"`
	// TargetHTTPSPort (passthrough mode) is the other proxy's TLS port (443).
	TargetHTTPSPort int    `json:"target_https_port,omitempty" gorm:"column:target_https_port"`
	TargetHostMAC   string `json:"target_host_mac,omitempty" gorm:"index;size:64"`
	TargetLabel     string `json:"target_label,omitempty" gorm:"size:255"`
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

	// Per-route request limits (http mode only; 0/false = off). These are the
	// per-route counterpart of the global read timeout: Traefik can't scope
	// the read timeout to a router, so routes bound abuse by concurrency,
	// rate, method and size instead. Column names are pinned — GORM splits
	// initialisms ("IP", "RPS") unpredictably.
	//
	// MaxConnsPerIP → inFlightReq (429 above N concurrent requests per client
	// IP) — the slowloris / connection-hoarding guard.
	MaxConnsPerIP int `json:"max_conns_per_ip" gorm:"column:max_conns_per_ip"`
	// RateLimitRPS → rateLimit (average req/s per client IP, burst 2×).
	RateLimitRPS int `json:"rate_limit_rps" gorm:"column:rate_limit_rps"`
	// ReadOnly restricts the router rule to GET/HEAD/OPTIONS — the upstream
	// never sees a request body at all.
	ReadOnly bool `json:"read_only" gorm:"column:read_only"`
	// UpstreamTimeoutSeconds → serversTransport.forwardingTimeouts.
	// responseHeaderTimeout (504 when the upstream doesn't answer in time).
	UpstreamTimeoutSeconds int `json:"upstream_timeout_seconds" gorm:"column:upstream_timeout_seconds"`
	// MaxBodyBytes → buffering.maxRequestBodyBytes (413 above the cap). The
	// buffering middleware buffers the request AND the response in full, so it
	// breaks streaming (SSE, long downloads) — meant for small admin UIs only.
	MaxBodyBytes int64 `json:"max_body_bytes" gorm:"column:max_body_bytes"`

	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// TableName pins the GORM table name.
func (Route) TableName() string { return "gateway_routes" }

// AutoMigrate creates the gateway tables (called from the central migrations).
func AutoMigrate(db *gorm.DB) error {
	return db.AutoMigrate(&Route{}, &Block{})
}

// IsWildcard reports whether the domain is a "*.example.com" wildcard.
func (r Route) IsWildcard() bool { return strings.HasPrefix(r.Domain, "*.") }

// IsPassthrough reports whether the route delegates TLS to another proxy.
func (r Route) IsPassthrough() bool { return r.Mode == RouteModePassthrough }

// HasLimits reports whether any per-route request limit is set.
func (r Route) HasLimits() bool {
	return r.MaxConnsPerIP > 0 || r.RateLimitRPS > 0 || r.ReadOnly || r.UpstreamTimeoutSeconds > 0 || r.MaxBodyBytes > 0
}

// DefaultRequestReadTimeoutSeconds is the entrypoint read timeout node-stats
// applies when the config leaves it unset: 24 h, so a big upload over a slow
// link goes through (Traefik's own default is 60 s).
const DefaultRequestReadTimeoutSeconds = 24 * 60 * 60

// RequestReadTimeoutUnlimited is the sentinel for "no read timeout at all".
const RequestReadTimeoutUnlimited = -1

// MaxRequestReadTimeoutSeconds bounds the configurable value (30 days).
const MaxRequestReadTimeoutSeconds = 30 * 24 * 60 * 60

// EffectiveRequestReadTimeoutSeconds resolves the configured value: the
// number of seconds Traefik gets, where 0 means unlimited (Traefik's own
// semantics for readTimeout=0).
func (c Config) EffectiveRequestReadTimeoutSeconds() int {
	switch {
	case c.RequestReadTimeoutSeconds == RequestReadTimeoutUnlimited:
		return 0
	case c.RequestReadTimeoutSeconds <= 0:
		return DefaultRequestReadTimeoutSeconds
	default:
		return c.RequestReadTimeoutSeconds
	}
}

// IsNode reports whether a host row (by MAC / stable system id) is the
// configured gateway node.
func (c Config) IsNode(mac, systemID, hardwareUUID string) bool {
	if c.NodeMAC == "" {
		return false
	}
	if mac != "" && strings.EqualFold(strings.TrimSpace(mac), strings.TrimSpace(c.NodeMAC)) {
		return true
	}
	if c.NodeSystemID == "" {
		return false
	}
	return (systemID != "" && systemID == c.NodeSystemID) || (hardwareUUID != "" && hardwareUUID == c.NodeSystemID)
}

// Protected reports whether the route has any access-control middleware.
func (r Route) Protected() bool {
	return r.BasicAuthUsers != "" || r.IPAllowList != ""
}

// PublicURL is the browser-facing URL of the route.
func (r Route) PublicURL(cfg Config) string {
	scheme := SchemeHTTP
	port := ""
	if r.TLS || r.IsPassthrough() {
		scheme = SchemeHTTPS
		if cfg.Mode == ModeManaged && cfg.HTTPSPort != 0 && cfg.HTTPSPort != 443 {
			port = ":" + itoa(cfg.HTTPSPort)
		}
	} else if cfg.Mode == ModeManaged && cfg.HTTPPort != 0 && cfg.HTTPPort != 80 {
		port = ":" + itoa(cfg.HTTPPort)
	}
	return scheme + "://" + r.Domain + port + r.PathPrefix
}
