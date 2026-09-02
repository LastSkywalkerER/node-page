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
	"fmt"
	"sort"
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

// BlocksFileName is the second owned file: the client deny list (see
// Rendered.Blocks). Same prefix so an operator's `ls` groups them.
const BlocksFileName = "node-stats-blocks.yml"

// CertResolverName is the ACME resolver id referenced by TLS routers.
const CertResolverName = "le"

// Target schemes.
const (
	SchemeHTTP  = "http"
	SchemeHTTPS = "https"
	// SchemeH2C: HTTP/2 cleartext to the upstream — what a gRPC service behind
	// the gateway needs (gRPC requires end-to-end HTTP/2; Traefik would
	// otherwise downgrade the hop to HTTP/1.1 and the stream breaks).
	SchemeH2C = "h2c"
)

// ValidScheme reports whether s is a supported target scheme.
func ValidScheme(s string) bool {
	return s == SchemeHTTP || s == SchemeHTTPS || s == SchemeH2C
}

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
	// RouteModeRedirect answers every request for the hostname(s) with a
	// redirect to another URL (www → apex, old domains, "moved" notices). No
	// upstream is involved (Traefik's noop@internal service).
	RouteModeRedirect = "redirect"
	// RouteModeStream forwards a raw TCP or UDP port (game servers, SSH,
	// databases) — a dedicated entrypoint per (protocol, port); no hostname.
	RouteModeStream = "stream"
)

// Stream protocols.
const (
	ProtoTCP = "tcp"
	ProtoUDP = "udp"
)

// Host-header modes (http mode).
const (
	// HostHeaderClient (default, ""): forward the client's Host header.
	HostHeaderClient = "client"
	// HostHeaderUpstream: send the target's own host:port as Host (Traefik
	// passHostHeader=false) — for upstreams that route by their own vhost.
	HostHeaderUpstream = "upstream"
	// HostHeaderCustom: send HostHeaderValue verbatim.
	HostHeaderCustom = "custom"
)

// Bounds for the list-ish route fields.
const (
	MaxAliases        = 16
	MaxExtraTargets   = 16
	MaxCustomHeaders  = 32
	MaxRetryAttempts  = 10
	MaxHealthInterval = 300
	// DefaultHealthCheckIntervalSeconds applies when a path is set but no interval.
	DefaultHealthCheckIntervalSeconds = 10
	// HSTSMaxAgeSeconds is the Strict-Transport-Security max-age rendered (2 years).
	HSTSMaxAgeSeconds = 63072000
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
	// NodeHostID is a request-only hint: the hosts.id (on the node serving the
	// UI) of the picked gateway host. SetConfig resolves MAC + stable id from
	// that row instead of looking the row up by MAC — with Docker bridge MACs
	// colliding across machines, a MAC lookup returns whatever local row holds
	// that MAC. Never persisted (zeroed before save).
	NodeHostID uint `json:"node_host_id,omitempty"`
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
	// Entrypoint hardening (managed mode; Traefik ≥ 3.7.12 — the managed image
	// / native binary are pinned to that line). Empty = node-stats default.
	//
	// AliasHeadersStrategy: what to do with request headers whose name aliases
	// another once a backend normalises it (X_Forwarded_For → X-Forwarded-For
	// in PHP/CGI/WSGI/nginx), which lets a client spoof the headers Traefik
	// sets. "delete" (default) drops them, "reject" answers 400, "keep"
	// forwards them (Traefik's own default).
	AliasHeadersStrategy string `json:"alias_headers_strategy,omitempty"`
	// EncodedPathPolicy: which percent-encoded specials are allowed in the
	// request path. "strict" (default) rejects %2F %5C %00 (path-confusion
	// against sloppy backends) and allows the rest; "permissive" allows all
	// (Traefik's default); "paranoid" rejects all seven.
	EncodedPathPolicy string `json:"encoded_path_policy,omitempty"`
	// DockerNetworks (managed mode, Docker backend) are EXISTING Docker
	// networks the Traefik container is additionally attached to, so the
	// containers on them are reachable by container name (`http://grafana:3000`)
	// — the way an NPM/Traefik that shares an app network does. Without it a
	// target must be a published host port; containers published on
	// 127.0.0.1 only are unreachable from another bridge network at all.
	// Ignored by the native (systemd) backend — there Traefik runs on the host.
	DockerNetworks []string `json:"docker_networks,omitempty"`
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
	// Several prefixes may be comma-separated ("/api,/oauth2") — they render as
	// one OR-ed rule and share the target (see PathPrefixes).
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

	// --- hostnames ---------------------------------------------------------
	// Aliases are extra hostnames (comma-separated) served by the same route
	// ("www.example.com" next to the apex). Every alias is OR-ed into the rule
	// and lands as a SAN on the same ACME certificate.
	Aliases string `json:"aliases,omitempty" gorm:"type:text"`

	// --- upstream shaping (http mode) ----------------------------------------
	// StripPrefix removes the matched PathPrefix(es) before forwarding
	// (/grafana → the app sees /); AddPrefix prepends a path instead/as well.
	StripPrefix bool   `json:"strip_prefix" gorm:"column:strip_prefix"`
	AddPrefix   string `json:"add_prefix,omitempty" gorm:"size:255;column:add_prefix"`
	// HostHeaderMode: HostHeader* ("" = client). HostHeaderValue is the custom
	// value for HostHeaderCustom.
	HostHeaderMode  string `json:"host_header_mode,omitempty" gorm:"size:16;column:host_header_mode"`
	HostHeaderValue string `json:"host_header_value,omitempty" gorm:"size:253;column:host_header_value"`
	// TargetServerName is the TLS SNI sent to an https upstream when it
	// differs from TargetHost (a vhost behind an IP).
	TargetServerName string `json:"target_server_name,omitempty" gorm:"size:253;column:target_server_name"`
	// ExtraTargets: additional "host:port" upstreams (newline/comma-separated,
	// same scheme) round-robined with the primary target — the same app on
	// several cluster nodes. HealthCheckPath (+ interval) lets Traefik drop a
	// failing server; Sticky pins a client to one server by cookie;
	// RetryAttempts re-sends a failed request to another server.
	ExtraTargets               string `json:"extra_targets,omitempty" gorm:"type:text;column:extra_targets"`
	HealthCheckPath            string `json:"health_check_path,omitempty" gorm:"size:255;column:health_check_path"`
	HealthCheckIntervalSeconds int    `json:"health_check_interval_seconds" gorm:"column:health_check_interval_seconds"`
	Sticky                     bool   `json:"sticky"`
	RetryAttempts              int    `json:"retry_attempts" gorm:"column:retry_attempts"`
	// RequestHeaders / ResponseHeaders: "Name: value" lines added to the
	// upstream request / the client response.
	RequestHeaders  string `json:"request_headers,omitempty" gorm:"type:text;column:request_headers"`
	ResponseHeaders string `json:"response_headers,omitempty" gorm:"type:text;column:response_headers"`

	// --- access control additions ----------------------------------------------
	// ForwardAuthURL: an SSO/auth service (Authelia, Authentik, Pocket-ID…)
	// every request is checked against first (Traefik forwardAuth);
	// ForwardAuthResponseHeaders (comma-separated) are copied from its 2xx
	// answer onto the upstream request (Remote-User…).
	ForwardAuthURL                string `json:"forward_auth_url,omitempty" gorm:"size:1024;column:forward_auth_url"`
	ForwardAuthResponseHeaders    string `json:"forward_auth_response_headers,omitempty" gorm:"type:text;column:forward_auth_response_headers"`
	ForwardAuthTrustForwardHeader bool   `json:"forward_auth_trust_forward_header" gorm:"column:forward_auth_trust_forward_header"`

	// --- response hardening / compression --------------------------------------
	// SecurityHeaders adds the usual browser hardening set (X-Frame-Options
	// SAMEORIGIN, nosniff, referrer policy). HSTS adds Strict-Transport-
	// Security (TLS routes only). Compress enables gzip/brotli/zstd.
	SecurityHeaders       bool `json:"security_headers" gorm:"column:security_headers"`
	HSTS                  bool `json:"hsts" gorm:"column:hsts"`
	HSTSIncludeSubdomains bool `json:"hsts_include_subdomains" gorm:"column:hsts_include_subdomains"`
	Compress              bool `json:"compress"`

	// --- redirect mode -----------------------------------------------------------
	RedirectURL          string `json:"redirect_url,omitempty" gorm:"size:1024;column:redirect_url"`
	RedirectPermanent    bool   `json:"redirect_permanent" gorm:"column:redirect_permanent"`
	RedirectPreservePath bool   `json:"redirect_preserve_path" gorm:"column:redirect_preserve_path"`

	// --- stream mode -------------------------------------------------------------
	// Protocol (tcp|udp) and ListenPort: the public port the gateway node
	// publishes and forwards to TargetHost:TargetPort (+ ExtraTargets).
	Protocol   string `json:"protocol,omitempty" gorm:"size:8"`
	ListenPort int    `json:"listen_port" gorm:"column:listen_port"`

	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// TableName pins the GORM table name.
func (Route) TableName() string { return "gateway_routes" }

// IsRedirect / IsStream report the non-proxy modes.
func (r Route) IsRedirect() bool { return r.Mode == RouteModeRedirect }
func (r Route) IsStream() bool   { return r.Mode == RouteModeStream }

// Hostnames returns the domain followed by its aliases (de-duplicated, blanks
// dropped). Empty for stream routes.
func (r Route) Hostnames() []string {
	var out []string
	seen := map[string]bool{}
	for _, h := range append([]string{r.Domain}, SplitCSV(r.Aliases)...) {
		h = strings.ToLower(strings.TrimSpace(h))
		if h == "" || seen[h] {
			continue
		}
		seen[h] = true
		out = append(out, h)
	}
	return out
}

// ExtraServers returns the additional "host:port" upstreams (newline- or
// comma-separated in ExtraTargets).
func (r Route) ExtraServers() []string {
	var out []string
	for _, line := range SplitLines(strings.ReplaceAll(r.ExtraTargets, ",", "\n")) {
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

// EffectiveHealthCheckInterval is the health-check interval in seconds (0 =
// no health check).
func (r Route) EffectiveHealthCheckInterval() int {
	if strings.TrimSpace(r.HealthCheckPath) == "" {
		return 0
	}
	if r.HealthCheckIntervalSeconds <= 0 {
		return DefaultHealthCheckIntervalSeconds
	}
	return r.HealthCheckIntervalSeconds
}

// StreamEntryPoint is the Traefik entrypoint name for a stream route:
// ns-<proto>-<port> (namespaced like every other object node-stats owns).
func StreamEntryPoint(proto string, port int) string {
	return fmt.Sprintf("%s%s-%d", namePrefix, proto, port)
}

// StreamPort is one (protocol, port) the managed Traefik must listen on.
type StreamPort struct {
	Protocol string `json:"protocol"`
	Port     int    `json:"port"`
}

// StreamPorts lists the distinct (protocol, port) pairs of the ENABLED stream
// routes, sorted — what the managed Traefik must publish as entrypoints.
func StreamPorts(routes []Route) []StreamPort {
	seen := map[StreamPort]bool{}
	var out []StreamPort
	for _, r := range routes {
		if !r.Enabled || !r.IsStream() || r.ListenPort <= 0 {
			continue
		}
		p := StreamPort{Protocol: r.Protocol, Port: r.ListenPort}
		if p.Protocol == "" {
			p.Protocol = ProtoTCP
		}
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Port != out[j].Port {
			return out[i].Port < out[j].Port
		}
		return out[i].Protocol < out[j].Protocol
	})
	return out
}

// AutoMigrate creates the gateway tables (called from the central migrations).
func AutoMigrate(db *gorm.DB) error {
	return db.AutoMigrate(&Route{}, &Block{})
}

// IsWildcard reports whether the domain is a "*.example.com" wildcard.
func (r Route) IsWildcard() bool { return strings.HasPrefix(r.Domain, "*.") }

// PathPrefixes returns the route's path prefixes (comma-separated in
// PathPrefix; "" or "/" → none).
func (r Route) PathPrefixes() []string {
	var out []string
	for _, p := range SplitCSV(r.PathPrefix) {
		if p != "" && p != "/" {
			out = append(out, p)
		}
	}
	return out
}

// FirstPathPrefix is the prefix used where only one fits (public URL).
func (r Route) FirstPathPrefix() string {
	if ps := r.PathPrefixes(); len(ps) > 0 {
		return ps[0]
	}
	return ""
}

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

// Hardening defaults (values validated against setup.AliasHeaders* /
// setup.EncodedPath*; kept as strings here so this package stays free of the
// setup import).
const (
	DefaultAliasHeadersStrategy = "delete"
	DefaultEncodedPathPolicy    = "strict"
)

// EffectiveAliasHeadersStrategy resolves the configured value (empty → default).
func (c Config) EffectiveAliasHeadersStrategy() string {
	if c.AliasHeadersStrategy == "" {
		return DefaultAliasHeadersStrategy
	}
	return c.AliasHeadersStrategy
}

// EffectiveEncodedPathPolicy resolves the configured value (empty → default).
func (c Config) EffectiveEncodedPathPolicy() string {
	if c.EncodedPathPolicy == "" {
		return DefaultEncodedPathPolicy
	}
	return c.EncodedPathPolicy
}

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
//
// The stable id wins whenever both sides have one: a MAC is NOT unique across
// machines for Docker bridge containers (every host derives the same
// 02:42:ac:11:00:02 from the same bridge IP), so a MAC-first check made a
// second Docker node believe it was the gateway too — it rendered the file,
// asked its controller for a Traefik and showed "this node is the gateway".
// MAC is only the fallback when the config or the host has no stable id.
func (c Config) IsNode(mac, systemID, hardwareUUID string) bool {
	if c.NodeMAC == "" {
		return false
	}
	systemID, hardwareUUID = strings.TrimSpace(systemID), strings.TrimSpace(hardwareUUID)
	if c.NodeSystemID != "" && (systemID != "" || hardwareUUID != "") {
		return systemID == c.NodeSystemID || hardwareUUID == c.NodeSystemID
	}
	return mac != "" && strings.EqualFold(strings.TrimSpace(mac), strings.TrimSpace(c.NodeMAC))
}

// Protected reports whether the route has any access-control middleware.
func (r Route) Protected() bool {
	return r.BasicAuthUsers != "" || r.IPAllowList != "" || strings.TrimSpace(r.ForwardAuthURL) != ""
}

// PublicURL is the browser-facing URL of the route. Stream routes have no
// hostname: "tcp://*:<port>" (the UI substitutes the gateway's address).
func (r Route) PublicURL(cfg Config) string {
	if r.IsStream() {
		proto := r.Protocol
		if proto == "" {
			proto = ProtoTCP
		}
		return proto + "://*:" + itoa(r.ListenPort)
	}
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
	return scheme + "://" + r.Domain + port + r.FirstPathPrefix()
}
