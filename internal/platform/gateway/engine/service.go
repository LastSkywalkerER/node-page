package engine

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/log"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"

	hosts "system-stats/internal/cluster/hosts"
	"system-stats/internal/platform/gateway"
	"system-stats/internal/platform/setup"
)

// ConfigStore persists the cluster-wide gateway.Config (cluster_config row; replicated
// via CmdConfigSet when Raft is on). Implemented in the raft package.
type ConfigStore interface {
	Get(ctx context.Context) (string, error)
	Set(ctx context.Context, value string) error
}

// Replicator is the subset of the raft replicator the gateway needs (declared
// here to break the raft → gateway import cycle).
type Replicator interface {
	Enabled() bool
	SubmitGatewayRouteUpsert(ctx context.Context, r gateway.Route) error
	SubmitGatewayRouteDelete(ctx context.Context, routeID string) error
	SubmitGatewayBlockUpsert(ctx context.Context, b gateway.Block) error
	SubmitGatewayBlockDelete(ctx context.Context, blockID string) error
}

// TargetSource lists the containers with published host ports across the
// cluster — the "pick a target" suggestions. Adapted over the docker service.
type TargetSource interface {
	Targets(ctx context.Context) ([]Target, error)
}

// Target is one suggested upstream (a container's published port on a host).
type Target struct {
	HostID    uint   `json:"host_id"`
	HostName  string `json:"host_name"`
	HostMAC   string `json:"host_mac"`
	IPv4      string `json:"ipv4"`
	App       string `json:"app"`
	Container string `json:"container,omitempty"`
	Port      int    `json:"port"`
	// PrivatePort is the in-container port (display only).
	PrivatePort int    `json:"private_port,omitempty"`
	Image       string `json:"image,omitempty"`
}

// BasicAuthInput is a plaintext credential the service hashes (bcrypt) before
// persisting. Password empty on update = keep the stored hash for that user.
type BasicAuthInput struct {
	User     string `json:"user"`
	Password string `json:"password"`
}

// RouteRequest is the create/update body.
type RouteRequest struct {
	Name                     string `json:"name"`
	Domain                   string `json:"domain"`
	PathPrefix               string `json:"path_prefix"`
	TargetScheme             string `json:"target_scheme"`
	TargetHost               string `json:"target_host"`
	TargetPort               int    `json:"target_port"`
	TargetHostMAC            string `json:"target_host_mac"`
	TargetLabel              string `json:"target_label"`
	TargetInsecureSkipVerify bool   `json:"target_insecure_skip_verify"`
	TLS                      bool   `json:"tls"`
	// Mode: "http" (default) or "passthrough" (delegate TLS to another proxy at
	// TargetHost — TargetPort = its http port, TargetHTTPSPort = its tls port).
	Mode            string           `json:"mode"`
	TargetHTTPSPort int              `json:"target_https_port"`
	BasicAuth       []BasicAuthInput `json:"basic_auth"`
	IPAllowList     string           `json:"ip_allow_list"`
	// Per-route request limits (http mode only; 0/false = off). See
	// gateway.Route for what each renders to.
	MaxConnsPerIP          int   `json:"max_conns_per_ip"`
	RateLimitRPS           int   `json:"rate_limit_rps"`
	ReadOnly               bool  `json:"read_only"`
	UpstreamTimeoutSeconds int   `json:"upstream_timeout_seconds"`
	MaxBodyBytes           int64 `json:"max_body_bytes"`
	Enabled                *bool `json:"enabled"`

	// Hostname aliases, upstream shaping, extra access control, response
	// hardening, redirect and stream fields — see gateway.Route.
	Aliases                       string `json:"aliases"`
	StripPrefix                   bool   `json:"strip_prefix"`
	AddPrefix                     string `json:"add_prefix"`
	HostHeaderMode                string `json:"host_header_mode"`
	HostHeaderValue               string `json:"host_header_value"`
	TargetServerName              string `json:"target_server_name"`
	ExtraTargets                  string `json:"extra_targets"`
	HealthCheckPath               string `json:"health_check_path"`
	HealthCheckIntervalSeconds    int    `json:"health_check_interval_seconds"`
	Sticky                        bool   `json:"sticky"`
	RetryAttempts                 int    `json:"retry_attempts"`
	RequestHeaders                string `json:"request_headers"`
	ResponseHeaders               string `json:"response_headers"`
	ForwardAuthURL                string `json:"forward_auth_url"`
	ForwardAuthResponseHeaders    string `json:"forward_auth_response_headers"`
	ForwardAuthTrustForwardHeader bool   `json:"forward_auth_trust_forward_header"`
	SecurityHeaders               bool   `json:"security_headers"`
	HSTS                          bool   `json:"hsts"`
	HSTSIncludeSubdomains         bool   `json:"hsts_include_subdomains"`
	Compress                      bool   `json:"compress"`
	RedirectURL                   string `json:"redirect_url"`
	RedirectPermanent             bool   `json:"redirect_permanent"`
	RedirectPreservePath          bool   `json:"redirect_preserve_path"`
	Protocol                      string `json:"protocol"`
	ListenPort                    int    `json:"listen_port"`
}

// Bounds for the per-route limits (sanity, not policy).
const (
	maxConnsPerIPLimit  = 100_000
	maxRateLimitRPS     = 1_000_000
	maxUpstreamTimeoutS = 24 * 60 * 60
	maxBodyBytesLimit   = int64(1) << 40 // 1 TiB
)

// RouteView is the API shape of a route (hashes stripped, users listed).
type RouteView struct {
	gateway.Route
	BasicAuthUsers []string `json:"basic_auth_users"`
	PublicURL      string   `json:"public_url"`
	Protected      bool     `json:"protected"`
	// TargetURL is the stored upstream; EffectiveURL is what Traefik on THIS
	// gateway node is actually given (e.g. host.docker.internal for a target on
	// the gateway host itself). Rewritten flags a difference.
	TargetURL    string `json:"target_url"`
	EffectiveURL string `json:"effective_url"`
	Rewritten    bool   `json:"rewritten"`
}

// Capabilities tells the UI what this node can do.
type Capabilities struct {
	// CanManage: this node can run a managed Traefik — via the docker
	// controller ("docker") or a systemd unit on a native root install
	// ("systemd"). ManageReason explains a false.
	CanManage    bool   `json:"can_manage"`
	ManageKind   string `json:"manage_kind"`
	ManageReason string `json:"manage_reason,omitempty"`
	// RunningInDocker / ManagedExternally explain why CanManage is false.
	RunningInDocker   bool   `json:"running_in_docker"`
	ManagedExternally bool   `json:"managed_externally"`
	LocalHostID       uint   `json:"local_host_id"`
	LocalMAC          string `json:"local_mac"`
	// ManagedDynamicDir is where this node would render in managed mode.
	ManagedDynamicDir string `json:"managed_dynamic_dir"`
}

// State is the GET /gateway response.
type State struct {
	Config       gateway.Config `json:"config"`
	Routes       []RouteView    `json:"routes"`
	Status       Status         `json:"status"`
	Capabilities Capabilities   `json:"capabilities"`
}

// Service is the admin-facing gateway API.
type Service interface {
	GetState(ctx context.Context) (*State, error)
	SetConfig(ctx context.Context, cfg gateway.Config) (*gateway.Config, error)
	CreateRoute(ctx context.Context, req RouteRequest) (*RouteView, error)
	UpdateRoute(ctx context.Context, routeID string, req RouteRequest) (*RouteView, error)
	DeleteRoute(ctx context.Context, routeID string) error
	Targets(ctx context.Context) ([]Target, error)
	// CheckTarget dials host:port from THIS node (TCP) — using the address
	// Traefik would actually be given here (same-host rewrite) — and returns
	// that checked address.
	CheckTarget(ctx context.Context, host, hostMAC string, port int) (*TargetCheck, error)
	// Logs returns the managed Traefik's recent logs (gateway node only).
	Logs(ctx context.Context, tail int) (string, error)
	Files(ctx context.Context) ([]ConfigFile, error)
	// CheckPublic asks an external service whether the gateway's HTTP/HTTPS
	// ports are reachable from the internet (from THIS node's public IP).
	CheckPublic(ctx context.Context, target string) (*PublicCheckResult, error)
	// Connections returns live connection stats (gateway node only; elsewhere
	// Available=false with reason "not_gateway" and the handler proxies).
	Connections(ctx context.Context, topN, recentN int) (*ConnectionsView, error)
	// GatewayNodeURL is the gateway node's dashboard URL for that proxying.
	GatewayNodeURL(ctx context.Context) string
	// Client blocks (IP/CIDR deny list rendered above every route).
	ListBlocks(ctx context.Context) ([]gateway.Block, error)
	CreateBlock(ctx context.Context, req BlockRequest) (*gateway.Block, error)
	DeleteBlock(ctx context.Context, blockID string) error
	// RunBlockExpiry sweeps expired blocks (call as a goroutine).
	RunBlockExpiry(ctx context.Context)
}

// ErrValidation marks a bad request (handler → 400).
var ErrValidation = errors.New("validation error")

type service struct {
	logger  *log.Logger
	repo    gateway.Repository
	blocks  gateway.BlockRepository
	cfg     ConfigStore
	hosts   hosts.Repository
	targets TargetSource
	raft    Replicator
	mat     *Materializer
}

// NewService wires the gateway service. mat may be nil (tests).
func NewService(logger *log.Logger, repo gateway.Repository, blocks gateway.BlockRepository, cfg ConfigStore, hostRepo hosts.Repository, targets TargetSource, raft Replicator, mat *Materializer) Service {
	return &service{logger: logger, repo: repo, blocks: blocks, cfg: cfg, hosts: hostRepo, targets: targets, raft: raft, mat: mat}
}

// LoadConfig decodes the stored gateway.Config (zero value when unset).
func LoadConfig(ctx context.Context, store ConfigStore) (gateway.Config, error) {
	raw, err := store.Get(ctx)
	if err != nil || strings.TrimSpace(raw) == "" {
		return gateway.Config{}, err
	}
	var cfg gateway.Config
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return gateway.Config{}, fmt.Errorf("gateway: corrupt config: %w", err)
	}
	return cfg, nil
}

func (s *service) GetState(ctx context.Context) (*State, error) {
	cfg, err := LoadConfig(ctx, s.cfg)
	if err != nil {
		return nil, err
	}
	rows, err := s.repo.List(ctx)
	if err != nil {
		return nil, err
	}
	views := make([]RouteView, 0, len(rows))
	for _, r := range rows {
		views = append(views, s.toView(ctx, r, cfg))
	}
	st := &State{Config: cfg, Routes: views}
	if s.mat != nil {
		st.Status = s.mat.Status()
		st.Capabilities = s.mat.Capabilities(ctx)
	}
	return st, nil
}

func (s *service) SetConfig(ctx context.Context, cfg gateway.Config) (*gateway.Config, error) {
	cfg.NodeMAC = strings.ToLower(strings.TrimSpace(cfg.NodeMAC))
	cfg.DynamicDir = strings.TrimSpace(cfg.DynamicDir)
	cfg.ACMEEmail = strings.TrimSpace(cfg.ACMEEmail)
	if cfg.Mode == "" {
		cfg.Mode = gateway.ModeManaged
	}
	if cfg.Mode != gateway.ModeManaged && cfg.Mode != gateway.ModeExternal {
		return nil, fmt.Errorf("%w: mode must be %q or %q", ErrValidation, gateway.ModeManaged, gateway.ModeExternal)
	}
	if cfg.Enabled {
		if cfg.NodeMAC == "" {
			return nil, fmt.Errorf("%w: pick the gateway node", ErrValidation)
		}
		if cfg.Mode == gateway.ModeExternal && cfg.DynamicDir == "" {
			return nil, fmt.Errorf("%w: external mode needs the Traefik dynamic-config directory", ErrValidation)
		}
		if cfg.ACMEEnabled && !strings.Contains(cfg.ACMEEmail, "@") {
			return nil, fmt.Errorf("%w: Let's Encrypt needs a contact e-mail", ErrValidation)
		}
	}
	if cfg.Mode == gateway.ModeManaged {
		if cfg.HTTPPort == 0 {
			cfg.HTTPPort = 80
		}
		if cfg.HTTPSPort == 0 {
			cfg.HTTPSPort = 443
		}
		if !validPort(cfg.HTTPPort) || !validPort(cfg.HTTPSPort) || cfg.HTTPPort == cfg.HTTPSPort {
			return nil, fmt.Errorf("%w: invalid gateway ports", ErrValidation)
		}
	}
	cfg.AliasHeadersStrategy = strings.ToLower(strings.TrimSpace(cfg.AliasHeadersStrategy))
	cfg.EncodedPathPolicy = strings.ToLower(strings.TrimSpace(cfg.EncodedPathPolicy))
	if !setup.ValidAliasHeadersStrategy(cfg.AliasHeadersStrategy) {
		return nil, fmt.Errorf("%w: alias headers strategy must be delete, reject or keep", ErrValidation)
	}
	if !setup.ValidEncodedPathPolicy(cfg.EncodedPathPolicy) {
		return nil, fmt.Errorf("%w: encoded path policy must be strict, permissive or paranoid", ErrValidation)
	}
	if t := cfg.RequestReadTimeoutSeconds; t < gateway.RequestReadTimeoutUnlimited || t > gateway.MaxRequestReadTimeoutSeconds {
		return nil, fmt.Errorf("%w: request read timeout must be -1 (unlimited), 0 (default 24h) or up to %d seconds", ErrValidation, gateway.MaxRequestReadTimeoutSeconds)
	}
	nets, err := normalizeDockerNetworks(cfg.DockerNetworks)
	if err != nil {
		return nil, err
	}
	cfg.DockerNetworks = nets
	cfg.NodeSystemID = ""
	if s.hosts != nil {
		var h *hosts.Host
		if cfg.NodeHostID != 0 {
			// Preferred: the exact row the admin picked (ids are local to the
			// node serving the UI, which is the node handling this request).
			row, err := s.hosts.GetHostByID(ctx, cfg.NodeHostID)
			if err != nil || row == nil {
				return nil, fmt.Errorf("%w: gateway node (host %d) not found", ErrValidation, cfg.NodeHostID)
			}
			h = row
			cfg.NodeMAC = strings.ToLower(strings.TrimSpace(h.MacAddress))
		} else if cfg.NodeMAC != "" {
			if row, err := s.hosts.GetHostByMacAddress(ctx, cfg.NodeMAC); err == nil && row != nil {
				h = row
			}
		}
		if h != nil {
			cfg.NodeName = h.Name
			cfg.NodeSystemID = h.SystemHostID
			if cfg.NodeSystemID == "" {
				cfg.NodeSystemID = h.HardwareUUID
			}
		}
	}
	cfg.NodeHostID = 0
	raw, err := json.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	if err := s.cfg.Set(ctx, string(raw)); err != nil {
		return nil, err
	}
	s.poke()
	return &cfg, nil
}

func (s *service) CreateRoute(ctx context.Context, req RouteRequest) (*RouteView, error) {
	r := gateway.Route{RouteID: newRouteID(), Enabled: true, CreatedAt: time.Now().UTC()}
	if err := s.applyRequest(&r, req, nil); err != nil {
		return nil, err
	}
	if err := s.checkDomainConflict(ctx, r); err != nil {
		return nil, err
	}
	if err := s.persist(ctx, r); err != nil {
		return nil, err
	}
	return s.view(ctx, r)
}

func (s *service) UpdateRoute(ctx context.Context, routeID string, req RouteRequest) (*RouteView, error) {
	existing, err := s.repo.GetByRouteID(ctx, routeID)
	if err != nil {
		return nil, err
	}
	r := *existing
	if err := s.applyRequest(&r, req, existing); err != nil {
		return nil, err
	}
	if err := s.checkDomainConflict(ctx, r); err != nil {
		return nil, err
	}
	if err := s.persist(ctx, r); err != nil {
		return nil, err
	}
	return s.view(ctx, r)
}

func (s *service) DeleteRoute(ctx context.Context, routeID string) error {
	if _, err := s.repo.GetByRouteID(ctx, routeID); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return err
	}
	var err error
	if s.raft != nil && s.raft.Enabled() {
		err = s.raft.SubmitGatewayRouteDelete(ctx, routeID)
	} else {
		err = s.repo.DeleteByRouteID(ctx, routeID)
	}
	if err == nil {
		s.poke()
	}
	return err
}

func (s *service) Targets(ctx context.Context) ([]Target, error) {
	if s.targets == nil {
		return []Target{}, nil
	}
	t, err := s.targets.Targets(ctx)
	if t == nil {
		t = []Target{}
	}
	return t, err
}

// TargetCheck is the reachability + protocol probe result for an upstream.
type TargetCheck struct {
	Checked   string `json:"checked"`
	Reachable bool   `json:"reachable"`
	Error     string `json:"error,omitempty"`
	// TLS reports whether the upstream completed a TLS handshake (so the route
	// must use scheme https); CertSubject / CertTrusted describe its cert.
	TLS         bool   `json:"tls"`
	CertSubject string `json:"cert_subject,omitempty"`
	CertTrusted bool   `json:"cert_trusted"`
}

func (s *service) CheckTarget(ctx context.Context, host, hostMAC string, port int) (*TargetCheck, error) {
	host = strings.TrimSpace(host)
	if host == "" || !validPort(port) {
		return nil, fmt.Errorf("%w: host and port are required", ErrValidation)
	}
	if s.mat != nil {
		host = s.mat.EffectiveTargetHost(ctx, host, hostMAC)
	}
	res := &TargetCheck{Checked: net.JoinHostPort(host, strconv.Itoa(port))}
	d := net.Dialer{Timeout: 3 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", res.Checked)
	if err != nil {
		res.Error = err.Error()
		return res, nil
	}
	_ = conn.Close()
	res.Reachable = true
	probeUpstreamTLS(ctx, res)
	return res, nil
}

// probeUpstreamTLS tries a TLS handshake against a reachable upstream. Success
// means the service speaks HTTPS on that port (a plain-http route would get
// "You're speaking plain HTTP to an SSL-enabled server port"); a failure that
// is not a timeout means plain HTTP.
func probeUpstreamTLS(ctx context.Context, res *TargetCheck) {
	rc, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	d := tls.Dialer{NetDialer: &net.Dialer{Timeout: 3 * time.Second}, Config: &tls.Config{InsecureSkipVerify: true}} // #nosec G402 — probe only
	conn, err := d.DialContext(rc, "tcp", res.Checked)
	if err != nil {
		return
	}
	defer conn.Close()
	tc, ok := conn.(*tls.Conn)
	if !ok {
		return
	}
	res.TLS = true
	if certs := tc.ConnectionState().PeerCertificates; len(certs) > 0 {
		res.CertSubject = certs[0].Subject.CommonName
		if res.CertSubject == "" && len(certs[0].DNSNames) > 0 {
			res.CertSubject = certs[0].DNSNames[0]
		}
		host, _, _ := net.SplitHostPort(res.Checked)
		opts := x509.VerifyOptions{DNSName: host, Intermediates: x509.NewCertPool()}
		for _, c := range certs[1:] {
			opts.Intermediates.AddCert(c)
		}
		if _, err := certs[0].Verify(opts); err == nil {
			res.CertTrusted = true
		}
	}
}

func (s *service) CheckPublic(ctx context.Context, target string) (*PublicCheckResult, error) {
	cfg, err := LoadConfig(ctx, s.cfg)
	if err != nil {
		return nil, err
	}
	hp, sp := cfg.HTTPPort, cfg.HTTPSPort
	if hp == 0 {
		hp = 80
	}
	if sp == 0 {
		sp = 443
	}
	res := PublicCheck(ctx, target, []int{hp, sp})
	return &res, nil
}

// Files lists the Traefik config files node-stats owns on this node.
func (s *service) Files(ctx context.Context) ([]ConfigFile, error) {
	if s.mat == nil {
		return nil, fmt.Errorf("%w: files unavailable", ErrValidation)
	}
	return s.mat.Files(ctx)
}

func (s *service) Logs(ctx context.Context, tail int) (string, error) {
	if s.mat == nil {
		return "", fmt.Errorf("%w: logs unavailable", ErrValidation)
	}
	return s.mat.Logs(ctx, tail)
}

// --- internals ---------------------------------------------------------------

var domainRe = regexp.MustCompile(`^(\*\.)?([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$|^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// applyRequest validates req and copies it onto r. prev (update) lets an
// empty password keep an existing user's hash.
func (s *service) applyRequest(r *gateway.Route, req RouteRequest, prev *gateway.Route) error {
	mode := strings.ToLower(strings.TrimSpace(req.Mode))
	if mode == "" {
		mode = gateway.RouteModeHTTP
	}
	switch mode {
	case gateway.RouteModeHTTP, gateway.RouteModePassthrough, gateway.RouteModeRedirect, gateway.RouteModeStream:
	default:
		return fmt.Errorf("%w: mode must be http, passthrough, redirect or stream", ErrValidation)
	}
	if mode == gateway.RouteModeStream {
		return s.applyStreamRequest(r, req)
	}
	domain := strings.ToLower(strings.TrimSpace(req.Domain))
	if domain == "" || !domainRe.MatchString(domain) {
		return fmt.Errorf("%w: domain must be a valid hostname (optionally *.wildcard)", ErrValidation)
	}
	wildcard := strings.HasPrefix(domain, "*.")
	if wildcard && mode != gateway.RouteModePassthrough && req.TLS {
		return fmt.Errorf("%w: a wildcard domain can't get a certificate via HTTP-01 — use passthrough mode (the other proxy issues certs) or turn HTTPS off", ErrValidation)
	}
	// Aliases: extra hostnames, same rules as the domain.
	var aliases []string
	seenHost := map[string]bool{domain: true}
	for _, a := range gateway.SplitCSV(strings.ToLower(req.Aliases)) {
		if !domainRe.MatchString(a) {
			return fmt.Errorf("%w: alias %q must be a valid hostname", ErrValidation, a)
		}
		if strings.HasPrefix(a, "*.") && mode != gateway.RouteModePassthrough && req.TLS {
			return fmt.Errorf("%w: wildcard alias %q can't get a certificate via HTTP-01", ErrValidation, a)
		}
		if !seenHost[a] {
			seenHost[a] = true
			aliases = append(aliases, a)
		}
	}
	if len(aliases) > gateway.MaxAliases {
		return fmt.Errorf("%w: at most %d aliases", ErrValidation, gateway.MaxAliases)
	}
	if mode == gateway.RouteModeRedirect {
		return s.applyRedirectRequest(r, req, domain, aliases)
	}
	if mode == gateway.RouteModePassthrough {
		if req.TargetHTTPSPort != 0 && !validPort(req.TargetHTTPSPort) {
			return fmt.Errorf("%w: target https port must be 1-65535", ErrValidation)
		}
		if strings.TrimSpace(req.PathPrefix) != "" && strings.TrimSpace(req.PathPrefix) != "/" {
			return fmt.Errorf("%w: passthrough routes can't have a path prefix (TLS is routed by SNI only)", ErrValidation)
		}
		if len(req.BasicAuth) > 0 || strings.TrimSpace(req.IPAllowList) != "" {
			return fmt.Errorf("%w: passthrough routes can't carry basic auth / IP allow lists — the traffic is encrypted end-to-end; configure access control on the other proxy", ErrValidation)
		}
	}
	// Path prefixes: one or several (comma-separated), each "/something";
	// a lone "/" means "whole host". Normalised back to a comma list.
	var prefixes []string
	seen := map[string]bool{}
	for _, p := range gateway.SplitCSV(req.PathPrefix) {
		if p == "" || p == "/" {
			continue
		}
		if !strings.HasPrefix(p, "/") || strings.ContainsAny(p, " `\"',") {
			return fmt.Errorf("%w: path prefix must start with / and contain no spaces or quotes", ErrValidation)
		}
		if !seen[p] {
			seen[p] = true
			prefixes = append(prefixes, p)
		}
	}
	path := strings.Join(prefixes, ",")
	scheme := strings.ToLower(strings.TrimSpace(req.TargetScheme))
	if scheme == "" {
		scheme = gateway.SchemeHTTP
	}
	if !gateway.ValidScheme(scheme) {
		return fmt.Errorf("%w: target scheme must be http, https or h2c (gRPC / HTTP/2 cleartext)", ErrValidation)
	}
	if scheme == gateway.SchemeH2C && mode == gateway.RouteModePassthrough {
		return fmt.Errorf("%w: passthrough routes forward raw TLS — the target scheme does not apply", ErrValidation)
	}
	th := strings.TrimSpace(req.TargetHost)
	if th == "" || strings.ContainsAny(th, " /`\"'") {
		return fmt.Errorf("%w: target host is required", ErrValidation)
	}
	if req.TargetPort == 0 && mode == gateway.RouteModePassthrough {
		req.TargetPort = 80
	}
	if !validPort(req.TargetPort) {
		return fmt.Errorf("%w: target port must be 1-65535", ErrValidation)
	}
	// Upstream shaping / headers / forward auth (http mode; passthrough drops them).
	addPrefix := strings.TrimSpace(req.AddPrefix)
	if addPrefix == "/" {
		addPrefix = ""
	}
	if addPrefix != "" && (!strings.HasPrefix(addPrefix, "/") || strings.ContainsAny(addPrefix, " `\"',")) {
		return fmt.Errorf("%w: add-prefix must start with / and contain no spaces or quotes", ErrValidation)
	}
	hostMode := strings.ToLower(strings.TrimSpace(req.HostHeaderMode))
	hostValue := strings.TrimSpace(req.HostHeaderValue)
	switch hostMode {
	case "", gateway.HostHeaderClient:
		hostMode, hostValue = "", ""
	case gateway.HostHeaderUpstream:
		hostValue = ""
	case gateway.HostHeaderCustom:
		if hostValue == "" || strings.ContainsAny(hostValue, " /`\"'\n") {
			return fmt.Errorf("%w: custom Host header needs a hostname value", ErrValidation)
		}
	default:
		return fmt.Errorf("%w: host header mode must be client, upstream or custom", ErrValidation)
	}
	serverName := strings.TrimSpace(req.TargetServerName)
	if serverName != "" && !domainRe.MatchString(strings.ToLower(serverName)) {
		return fmt.Errorf("%w: upstream SNI server name must be a hostname", ErrValidation)
	}
	extra, err := normalizeExtraTargets(req.ExtraTargets)
	if err != nil {
		return err
	}
	hcPath := strings.TrimSpace(req.HealthCheckPath)
	if hcPath != "" && (!strings.HasPrefix(hcPath, "/") || strings.ContainsAny(hcPath, " `\"'")) {
		return fmt.Errorf("%w: health-check path must start with /", ErrValidation)
	}
	if req.HealthCheckIntervalSeconds < 0 || req.HealthCheckIntervalSeconds > gateway.MaxHealthInterval {
		return fmt.Errorf("%w: health-check interval must be 0 (default) to %d seconds", ErrValidation, gateway.MaxHealthInterval)
	}
	if req.RetryAttempts < 0 || req.RetryAttempts > gateway.MaxRetryAttempts {
		return fmt.Errorf("%w: retry attempts must be 0 (off) to %d", ErrValidation, gateway.MaxRetryAttempts)
	}
	reqHeaders, err := normalizeHeaderLines(req.RequestHeaders, "request")
	if err != nil {
		return err
	}
	respHeaders, err := normalizeHeaderLines(req.ResponseHeaders, "response")
	if err != nil {
		return err
	}
	fwdAuth := strings.TrimSpace(req.ForwardAuthURL)
	if fwdAuth != "" {
		u, err := url.Parse(fwdAuth)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fmt.Errorf("%w: forward-auth address must be an http(s) URL", ErrValidation)
		}
	}
	var fwdHeaders []string
	for _, h := range gateway.SplitCSV(req.ForwardAuthResponseHeaders) {
		if !headerNameRe.MatchString(h) {
			return fmt.Errorf("%w: invalid forward-auth response header name %q", ErrValidation, h)
		}
		fwdHeaders = append(fwdHeaders, h)
	}
	if req.MaxConnsPerIP < 0 || req.MaxConnsPerIP > maxConnsPerIPLimit {
		return fmt.Errorf("%w: max concurrent requests per IP must be 0 (off) to %d", ErrValidation, maxConnsPerIPLimit)
	}
	if req.RateLimitRPS < 0 || req.RateLimitRPS > maxRateLimitRPS {
		return fmt.Errorf("%w: rate limit must be 0 (off) to %d req/s", ErrValidation, maxRateLimitRPS)
	}
	if req.UpstreamTimeoutSeconds < 0 || req.UpstreamTimeoutSeconds > maxUpstreamTimeoutS {
		return fmt.Errorf("%w: upstream response timeout must be 0 (off) to %d seconds", ErrValidation, maxUpstreamTimeoutS)
	}
	if req.MaxBodyBytes < 0 || req.MaxBodyBytes > maxBodyBytesLimit {
		return fmt.Errorf("%w: max request body must be 0 (unlimited) to %d bytes", ErrValidation, maxBodyBytesLimit)
	}
	allow := strings.TrimSpace(req.IPAllowList)
	for _, c := range gateway.SplitCSV(allow) {
		if _, _, err := net.ParseCIDR(c); err != nil {
			if net.ParseIP(c) == nil {
				return fmt.Errorf("%w: invalid CIDR %q in IP allow list", ErrValidation, c)
			}
		}
	}

	// Basic auth: hash new passwords; keep the previous hash when the password
	// is blank for a user that already existed.
	prevHashes := map[string]string{}
	if prev != nil {
		for _, line := range gateway.SplitLines(prev.BasicAuthUsers) {
			if i := strings.IndexByte(line, ':'); i > 0 {
				prevHashes[line[:i]] = line
			}
		}
	}
	var lines []string
	for _, in := range req.BasicAuth {
		u := strings.TrimSpace(in.User)
		if u == "" {
			continue
		}
		if strings.ContainsAny(u, ": \n") {
			return fmt.Errorf("%w: basic-auth user names cannot contain ':' or spaces", ErrValidation)
		}
		if in.Password == "" {
			if line, ok := prevHashes[u]; ok {
				lines = append(lines, line)
				continue
			}
			return fmt.Errorf("%w: password required for new basic-auth user %q", ErrValidation, u)
		}
		h, err := bcrypt.GenerateFromPassword([]byte(in.Password), bcrypt.DefaultCost)
		if err != nil {
			return err
		}
		lines = append(lines, u+":"+string(h))
	}

	r.Name = strings.TrimSpace(req.Name)
	if r.Name == "" {
		r.Name = domain
	}
	r.Mode = mode
	r.TargetHTTPSPort = 0
	if mode == gateway.RouteModePassthrough {
		r.TargetHTTPSPort = req.TargetHTTPSPort
		if r.TargetHTTPSPort == 0 {
			r.TargetHTTPSPort = 443
		}
		if req.TargetPort == 0 {
			req.TargetPort = 80
		}
	}
	r.Domain = domain
	r.PathPrefix = path
	r.TargetScheme = scheme
	r.TargetHost = th
	r.TargetPort = req.TargetPort
	r.TargetHostMAC = strings.ToLower(strings.TrimSpace(req.TargetHostMAC))
	r.TargetLabel = strings.TrimSpace(req.TargetLabel)
	r.TargetInsecureSkipVerify = req.TargetInsecureSkipVerify && scheme == gateway.SchemeHTTPS
	r.TLS = req.TLS
	r.BasicAuthUsers = strings.Join(lines, "\n")
	r.IPAllowList = strings.Join(gateway.SplitCSV(allow), ",")
	r.Aliases = strings.Join(aliases, ",")
	r.RedirectURL, r.RedirectPermanent, r.RedirectPreservePath = "", false, false
	r.Protocol, r.ListenPort = "", 0
	// Everything below is an HTTP-layer feature — a passthrough route is an
	// opaque TLS stream and carries none of it.
	r.StripPrefix, r.AddPrefix = false, ""
	r.HostHeaderMode, r.HostHeaderValue, r.TargetServerName = "", "", ""
	r.ExtraTargets, r.HealthCheckPath, r.HealthCheckIntervalSeconds, r.Sticky, r.RetryAttempts = "", "", 0, false, 0
	r.RequestHeaders, r.ResponseHeaders = "", ""
	r.ForwardAuthURL, r.ForwardAuthResponseHeaders, r.ForwardAuthTrustForwardHeader = "", "", false
	r.SecurityHeaders, r.HSTS, r.HSTSIncludeSubdomains, r.Compress = false, false, false, false
	if mode == gateway.RouteModeHTTP {
		r.StripPrefix = req.StripPrefix && path != ""
		r.AddPrefix = addPrefix
		r.HostHeaderMode, r.HostHeaderValue = hostMode, hostValue
		if scheme == gateway.SchemeHTTPS {
			r.TargetServerName = serverName
		}
		r.ExtraTargets = strings.Join(extra, "\n")
		r.HealthCheckPath = hcPath
		if hcPath != "" {
			r.HealthCheckIntervalSeconds = req.HealthCheckIntervalSeconds
		}
		r.Sticky = req.Sticky
		r.RetryAttempts = req.RetryAttempts
		r.RequestHeaders = strings.Join(reqHeaders, "\n")
		r.ResponseHeaders = strings.Join(respHeaders, "\n")
		r.ForwardAuthURL = fwdAuth
		if fwdAuth != "" {
			r.ForwardAuthResponseHeaders = strings.Join(fwdHeaders, ",")
			r.ForwardAuthTrustForwardHeader = req.ForwardAuthTrustForwardHeader
		}
		r.SecurityHeaders = req.SecurityHeaders
		r.HSTS = req.HSTS && req.TLS
		r.HSTSIncludeSubdomains = r.HSTS && req.HSTSIncludeSubdomains
		r.Compress = req.Compress
	}
	// Limits are HTTP-layer features; a passthrough route is an opaque TLS
	// stream, so they are dropped there (the form hides them as well).
	r.MaxConnsPerIP, r.RateLimitRPS, r.ReadOnly, r.UpstreamTimeoutSeconds, r.MaxBodyBytes = 0, 0, false, 0, 0
	if mode == gateway.RouteModeHTTP {
		r.MaxConnsPerIP = req.MaxConnsPerIP
		r.RateLimitRPS = req.RateLimitRPS
		r.ReadOnly = req.ReadOnly
		r.UpstreamTimeoutSeconds = req.UpstreamTimeoutSeconds
		r.MaxBodyBytes = req.MaxBodyBytes
	}
	if req.Enabled != nil {
		r.Enabled = *req.Enabled
	}
	return nil
}

// dockerNetworkRe matches what `docker network create` accepts as a name.
var dockerNetworkRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$`)

// normalizeDockerNetworks trims, de-duplicates and validates the extra Docker
// network names of the managed Traefik ("default" is implicit and rejected so
// the compose stanza never lists it twice).
func normalizeDockerNetworks(in []string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	for _, raw := range in {
		n := strings.TrimSpace(raw)
		if n == "" || seen[n] {
			continue
		}
		if n == "default" || !dockerNetworkRe.MatchString(n) {
			return nil, fmt.Errorf("%w: invalid Docker network name %q", ErrValidation, raw)
		}
		seen[n] = true
		out = append(out, n)
	}
	return out, nil
}

// checkDomainConflict rejects a second route on the same hostname+path (any
// of the route's hostnames — domain or alias — against any of the other's),
// and a second stream on the same protocol+port.
func (s *service) checkDomainConflict(ctx context.Context, r gateway.Route) error {
	rows, err := s.repo.List(ctx)
	if err != nil {
		return err
	}
	if r.IsStream() {
		for _, o := range rows {
			if o.RouteID != r.RouteID && o.IsStream() && o.ListenPort == r.ListenPort && o.Protocol == r.Protocol {
				return fmt.Errorf("%w: %s port %d is already forwarded (%s)", ErrValidation, r.Protocol, r.ListenPort, o.Name)
			}
		}
		return nil
	}
	mine := r.PathPrefixes()
	for _, o := range rows {
		if o.RouteID == r.RouteID || o.IsStream() {
			continue
		}
		shared := ""
		for _, a := range r.Hostnames() {
			for _, b := range o.Hostnames() {
				if a == b {
					shared = a
				}
			}
		}
		if shared == "" {
			continue
		}
		theirs := o.PathPrefixes()
		if len(mine) == 0 && len(theirs) == 0 {
			return fmt.Errorf("%w: %s is already routed (%s)", ErrValidation, shared, o.Name)
		}
		// Two routes may share a hostname only while their prefix sets are disjoint.
		for _, a := range mine {
			for _, b := range theirs {
				if a == b {
					return fmt.Errorf("%w: %s%s is already routed (%s)", ErrValidation, shared, a, o.Name)
				}
			}
		}
	}
	return nil
}

// applyRedirectRequest validates a redirect route: hostnames + a target URL,
// no upstream.
func (s *service) applyRedirectRequest(r *gateway.Route, req RouteRequest, domain string, aliases []string) error {
	target := strings.TrimSpace(req.RedirectURL)
	u, err := url.Parse(target)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || strings.ContainsAny(target, " `\"'\n$") {
		return fmt.Errorf("%w: redirect target must be an http(s) URL", ErrValidation)
	}
	for _, h := range append([]string{domain}, aliases...) {
		if strings.EqualFold(u.Hostname(), h) {
			return fmt.Errorf("%w: redirect target points back at %s — that would loop", ErrValidation, h)
		}
	}
	*r = gateway.Route{ID: r.ID, RouteID: r.RouteID, CreatedAt: r.CreatedAt, Enabled: r.Enabled}
	r.Name = strings.TrimSpace(req.Name)
	if r.Name == "" {
		r.Name = domain + " → " + u.Host
	}
	r.Mode = gateway.RouteModeRedirect
	r.Domain = domain
	r.Aliases = strings.Join(aliases, ",")
	r.TargetScheme = gateway.SchemeHTTP
	// TargetHost/Port are NOT NULL columns with no meaning here — keep a
	// harmless placeholder so the row round-trips through every store.
	r.TargetHost, r.TargetPort = u.Hostname(), 80
	r.TLS = req.TLS
	r.RedirectURL = target
	r.RedirectPermanent = req.RedirectPermanent
	r.RedirectPreservePath = req.RedirectPreservePath
	if req.Enabled != nil {
		r.Enabled = *req.Enabled
	}
	return nil
}

// applyStreamRequest validates a raw TCP/UDP forward: protocol + public port
// + target(s); no hostname, no HTTP features.
func (s *service) applyStreamRequest(r *gateway.Route, req RouteRequest) error {
	proto := strings.ToLower(strings.TrimSpace(req.Protocol))
	if proto == "" {
		proto = gateway.ProtoTCP
	}
	if proto != gateway.ProtoTCP && proto != gateway.ProtoUDP {
		return fmt.Errorf("%w: stream protocol must be tcp or udp", ErrValidation)
	}
	if !validPort(req.ListenPort) {
		return fmt.Errorf("%w: public port must be 1-65535", ErrValidation)
	}
	if cfg, err := LoadConfig(context.Background(), s.cfg); err == nil {
		hp, sp := cfg.HTTPPort, cfg.HTTPSPort
		if hp == 0 {
			hp = 80
		}
		if sp == 0 {
			sp = 443
		}
		if req.ListenPort == hp || req.ListenPort == sp || req.ListenPort == 8082 {
			return fmt.Errorf("%w: port %d is used by the gateway itself (http/https/ping)", ErrValidation, req.ListenPort)
		}
	}
	th := strings.TrimSpace(req.TargetHost)
	if th == "" || strings.ContainsAny(th, " /`\"'") {
		return fmt.Errorf("%w: target host is required", ErrValidation)
	}
	if !validPort(req.TargetPort) {
		return fmt.Errorf("%w: target port must be 1-65535", ErrValidation)
	}
	extra, err := normalizeExtraTargets(req.ExtraTargets)
	if err != nil {
		return err
	}
	*r = gateway.Route{ID: r.ID, RouteID: r.RouteID, CreatedAt: r.CreatedAt, Enabled: r.Enabled}
	r.Name = strings.TrimSpace(req.Name)
	if r.Name == "" {
		r.Name = fmt.Sprintf("%s/%d", proto, req.ListenPort)
	}
	r.Mode = gateway.RouteModeStream
	r.Protocol = proto
	r.ListenPort = req.ListenPort
	r.TargetScheme = proto
	r.TargetHost = th
	r.TargetPort = req.TargetPort
	r.TargetHostMAC = strings.ToLower(strings.TrimSpace(req.TargetHostMAC))
	r.TargetLabel = strings.TrimSpace(req.TargetLabel)
	r.ExtraTargets = strings.Join(extra, "\n")
	if req.Enabled != nil {
		r.Enabled = *req.Enabled
	}
	return nil
}

var headerNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{0,127}$`)

// normalizeHeaderLines validates "Name: value" lines (kind is for the error).
func normalizeHeaderLines(s, kind string) ([]string, error) {
	var out []string
	for _, line := range gateway.SplitLines(s) {
		i := strings.IndexByte(line, ':')
		if i <= 0 {
			return nil, fmt.Errorf("%w: %s header %q must look like \"Name: value\"", ErrValidation, kind, line)
		}
		name, value := strings.TrimSpace(line[:i]), strings.TrimSpace(line[i+1:])
		if !headerNameRe.MatchString(name) {
			return nil, fmt.Errorf("%w: invalid %s header name %q", ErrValidation, kind, name)
		}
		if strings.ContainsAny(value, "\r\n") {
			return nil, fmt.Errorf("%w: %s header %q value must be one line", ErrValidation, kind, name)
		}
		out = append(out, name+": "+value)
	}
	if len(out) > gateway.MaxCustomHeaders {
		return nil, fmt.Errorf("%w: at most %d %s headers", ErrValidation, gateway.MaxCustomHeaders, kind)
	}
	return out, nil
}

// normalizeExtraTargets validates "host:port" entries (newline/comma-separated).
func normalizeExtraTargets(s string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	for _, item := range gateway.SplitLines(strings.ReplaceAll(s, ",", "\n")) {
		host, portStr, err := net.SplitHostPort(item)
		if err != nil || host == "" || strings.ContainsAny(host, " /`\"'") {
			return nil, fmt.Errorf("%w: extra target %q must be host:port", ErrValidation, item)
		}
		port, err := strconv.Atoi(portStr)
		if err != nil || !validPort(port) {
			return nil, fmt.Errorf("%w: extra target %q has an invalid port", ErrValidation, item)
		}
		key := net.JoinHostPort(host, portStr)
		if !seen[key] {
			seen[key] = true
			out = append(out, key)
		}
	}
	if len(out) > gateway.MaxExtraTargets {
		return nil, fmt.Errorf("%w: at most %d extra targets", ErrValidation, gateway.MaxExtraTargets)
	}
	return out, nil
}

func (s *service) persist(ctx context.Context, r gateway.Route) error {
	var err error
	if s.raft != nil && s.raft.Enabled() {
		err = s.raft.SubmitGatewayRouteUpsert(ctx, r)
	} else {
		err = s.repo.Upsert(ctx, &r)
	}
	if err == nil {
		s.poke()
	}
	return err
}

func (s *service) view(ctx context.Context, r gateway.Route) (*RouteView, error) {
	cfg, _ := LoadConfig(ctx, s.cfg)
	if stored, err := s.repo.GetByRouteID(ctx, r.RouteID); err == nil {
		r = *stored
	}
	v := s.toView(ctx, r, cfg)
	return &v, nil
}

func (s *service) poke() {
	if s.mat != nil {
		s.mat.Poke()
	}
}

func (s *service) toView(ctx context.Context, r gateway.Route, cfg gateway.Config) RouteView {
	users := []string{}
	for _, line := range gateway.SplitLines(r.BasicAuthUsers) {
		if i := strings.IndexByte(line, ':'); i > 0 {
			users = append(users, line[:i])
		}
	}
	v := RouteView{Route: r, BasicAuthUsers: users, PublicURL: r.PublicURL(cfg), Protected: r.Protected(), TargetURL: targetURL(r)}
	v.EffectiveURL = v.TargetURL
	if s.mat != nil {
		v.EffectiveURL, v.Rewritten = s.mat.EffectiveTargetURL(ctx, cfg, r)
	}
	return v
}

func validPort(p int) bool { return p > 0 && p <= 65535 }

func newRouteID() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
