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
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/log"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"

	hosts "system-stats/internal/cluster/hosts"
	"system-stats/internal/platform/gateway"
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
	Enabled         *bool            `json:"enabled"`
}

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
	cfg.NodeSystemID = ""
	if cfg.NodeMAC != "" && s.hosts != nil {
		if h, err := s.hosts.GetHostByMacAddress(ctx, cfg.NodeMAC); err == nil && h != nil {
			cfg.NodeName = h.Name
			cfg.NodeSystemID = h.SystemHostID
			if cfg.NodeSystemID == "" {
				cfg.NodeSystemID = h.HardwareUUID
			}
		}
	}
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
	domain := strings.ToLower(strings.TrimSpace(req.Domain))
	if domain == "" || !domainRe.MatchString(domain) {
		return fmt.Errorf("%w: domain must be a valid hostname (optionally *.wildcard)", ErrValidation)
	}
	mode := strings.ToLower(strings.TrimSpace(req.Mode))
	if mode == "" {
		mode = gateway.RouteModeHTTP
	}
	if mode != gateway.RouteModeHTTP && mode != gateway.RouteModePassthrough {
		return fmt.Errorf("%w: mode must be http or passthrough", ErrValidation)
	}
	wildcard := strings.HasPrefix(domain, "*.")
	if wildcard && mode == gateway.RouteModeHTTP && req.TLS {
		return fmt.Errorf("%w: a wildcard domain can't get a certificate via HTTP-01 — use passthrough mode (the other proxy issues certs) or turn HTTPS off", ErrValidation)
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
	path := strings.TrimSpace(req.PathPrefix)
	if path == "/" {
		path = ""
	}
	if path != "" && (!strings.HasPrefix(path, "/") || strings.ContainsAny(path, " `\"'")) {
		return fmt.Errorf("%w: path prefix must start with / and contain no spaces or quotes", ErrValidation)
	}
	scheme := strings.ToLower(strings.TrimSpace(req.TargetScheme))
	if scheme == "" {
		scheme = gateway.SchemeHTTP
	}
	if scheme != gateway.SchemeHTTP && scheme != gateway.SchemeHTTPS {
		return fmt.Errorf("%w: target scheme must be http or https", ErrValidation)
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
	if req.Enabled != nil {
		r.Enabled = *req.Enabled
	}
	return nil
}

// checkDomainConflict rejects a second route on the same domain+path.
func (s *service) checkDomainConflict(ctx context.Context, r gateway.Route) error {
	rows, err := s.repo.List(ctx)
	if err != nil {
		return err
	}
	for _, o := range rows {
		if o.RouteID != r.RouteID && o.Domain == r.Domain && o.PathPrefix == r.PathPrefix {
			return fmt.Errorf("%w: %s%s is already routed (%s)", ErrValidation, r.Domain, r.PathPrefix, o.Name)
		}
	}
	return nil
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
