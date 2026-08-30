package engine

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/log"

	hosts "system-stats/internal/cluster/hosts"
	"system-stats/internal/platform/gateway"
	"system-stats/internal/platform/setup"
)

// reconcileInterval bounds how stale the rendered file can be after a change
// that arrived via Raft (local changes Poke() immediately).
const reconcileInterval = 5 * time.Second

// traefikPingURL is the managed Traefik's ping endpoint (compose service name
// `traefik`, ping entrypoint :8082 — see setup.BuildComposeContent).
const traefikPingURL = "http://traefik:8082/ping"

// Status is this node's view of the gateway runtime.
type Status struct {
	IsGatewayNode bool   `json:"is_gateway_node"`
	Mode          string `json:"mode,omitempty"`
	// FilePath is the dynamic file this node renders (gateway node only).
	FilePath     string     `json:"file_path,omitempty"`
	RouteCount   int        `json:"route_count"`
	LastRenderAt *time.Time `json:"last_render_at,omitempty"`
	LastError    string     `json:"last_error,omitempty"`
	// Controller mirrors controller-status.json (managed mode on the gateway node).
	Controller *setup.ControllerStatus `json:"controller,omitempty"`
	// TraefikHealthy is the managed Traefik's ping result (nil = not probed).
	TraefikHealthy *bool `json:"traefik_healthy,omitempty"`
	// TraefikDetail is the systemd unit state / last journal lines (native).
	TraefikDetail string `json:"traefik_detail,omitempty"`
}

// MaterializerDeps are the Materializer's collaborators.
type MaterializerDeps struct {
	Logger *log.Logger
	Repo   gateway.Repository
	Config ConfigStore
	Hosts  hosts.Repository
	// DataDir is NODE_STATS_DATA_DIR (/app/data) — managed-mode dynamic dir
	// lives under it (bind-mounted into the Traefik container by compose).
	DataDir string
	// DockerLogs fetches the managed Traefik container's logs (docker mode).
	DockerLogs func(ctx context.Context, project string, tail int) (string, error)
	// DBType / DBDSN describe the running engine so a freshly created
	// desired-state keeps the same DB topology (mirrors setup.RequestRecreate).
	DBType string
	DBDSN  string
}

// Materializer reconciles on-disk reality (Traefik dynamic file, controller
// desired-state) from the replicated gateway state. Runs on EVERY node; only
// the node whose local MAC matches gateway.Config.NodeMAC renders anything.
type Materializer struct {
	deps   MaterializerDeps
	poke   chan struct{}
	native *nativeProvisioner

	mu     sync.Mutex
	status Status
	// lastPath is the file we last wrote (persisted so a restart still cleans
	// up when the gateway moved elsewhere in the meantime).
	lastPath string
}

// NewMaterializer creates the reconciler (call Run in a goroutine).
func NewMaterializer(deps MaterializerDeps) *Materializer {
	if deps.DataDir == "" {
		deps.DataDir = "/app/data"
	}
	m := &Materializer{deps: deps, poke: make(chan struct{}, 1)}
	m.lastPath = m.readLastPath()
	// Managed-mode backends: Docker → controller sidecar (desired-state);
	// native root install (deb / Proxmox LXC) → node-stats installs Traefik and
	// drives a systemd unit (native.go). External mode only writes the file.
	if ok, _ := nativeAvailable(); ok && !setup.RunningInDocker() {
		m.native = newNativeProvisioner(deps.Logger, deps.DataDir)
	}
	return m
}

// Poke requests an immediate reconcile (after a local mutation).
func (m *Materializer) Poke() {
	select {
	case m.poke <- struct{}{}:
	default:
	}
}

// Status returns the last reconcile outcome.
func (m *Materializer) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.status
}

// ManagedDynamicDir is where managed mode renders (inside the app container).
func (m *Materializer) ManagedDynamicDir() string {
	return filepath.Join(m.deps.DataDir, "traefik", "dynamic")
}

// Capabilities reports what this node can do for the UI.
func (m *Materializer) Capabilities(ctx context.Context) Capabilities {
	caps := Capabilities{
		RunningInDocker:   setup.RunningInDocker(),
		ManagedExternally: setup.ManagedExternally(),
		LocalHostID:       hosts.LocalCollectorHostID,
		ManagedDynamicDir: m.ManagedDynamicDir(),
	}
	caps.ManageKind, caps.ManageReason = m.manageKind()
	caps.CanManage = caps.ManageKind != ""
	caps.LocalMAC = m.localMAC(ctx)
	return caps
}

// Run loops until ctx is done.
func (m *Materializer) Run(ctx context.Context) {
	t := time.NewTicker(reconcileInterval)
	defer t.Stop()
	m.reconcile(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-m.poke:
		case <-t.C:
		}
		m.reconcile(ctx)
	}
}

// manageKind decides which managed backend this node can drive: "docker"
// (controller sidecar), "systemd" (native root install), or "" with a reason.
func (m *Materializer) manageKind() (kind, reason string) {
	if setup.RunningInDocker() {
		if setup.ManagedExternally() {
			return "", "this node's compose stack is managed externally (Dokploy / orchestrator) — node-stats can't add a Traefik service; use external mode"
		}
		return "docker", ""
	}
	if m.native != nil {
		return "systemd", ""
	}
	_, why := nativeAvailable()
	return "", why
}

// localizeTargets rewrites targets that live on the gateway host itself when
// Traefik runs as a container here: a port published on this host is reached
// from inside the container as host.docker.internal:<port> (compose adds the
// host-gateway alias), while the host's own LAN IP may or may not hairpin
// back through the bridge. Routes keep the real IP in the DB, so moving the
// gateway to another node renders the routable address again.
func (m *Materializer) localizeTargets(ctx context.Context, cfg gateway.Config, routes []gateway.Route) []gateway.Route {
	if cfg.Mode != gateway.ModeManaged || !setup.RunningInDocker() || m.deps.Hosts == nil {
		return routes
	}
	h, err := m.deps.Hosts.GetHostByID(ctx, hosts.LocalCollectorHostID)
	if err != nil || h == nil {
		return routes
	}
	out := make([]gateway.Route, len(routes))
	for i, r := range routes {
		out[i] = m.effectiveRoute(cfg, h, r)
	}
	return out
}

// effectiveRoute returns the route as it is actually rendered on THIS node.
func (m *Materializer) effectiveRoute(cfg gateway.Config, local *hosts.Host, r gateway.Route) gateway.Route {
	if cfg.Mode != gateway.ModeManaged || !setup.RunningInDocker() || local == nil {
		return r
	}
	if (r.TargetHostMAC != "" && strings.EqualFold(r.TargetHostMAC, local.MacAddress)) ||
		(local.IPv4 != "" && r.TargetHost == local.IPv4) || r.TargetHost == "127.0.0.1" || r.TargetHost == "localhost" {
		r.TargetHost = "host.docker.internal"
	}
	return r
}

// EffectiveTargetHost applies the same-host rewrite to a bare host (+ optional
// host MAC) the way the renderer would for a route with those fields.
func (m *Materializer) EffectiveTargetHost(ctx context.Context, host, hostMAC string) string {
	cfg, err := LoadConfig(ctx, m.deps.Config)
	if err != nil || m.deps.Hosts == nil {
		return host
	}
	h, err := m.deps.Hosts.GetHostByID(ctx, hosts.LocalCollectorHostID)
	if err != nil || h == nil {
		return host
	}
	return m.effectiveRoute(cfg, h, gateway.Route{TargetHost: host, TargetHostMAC: hostMAC}).TargetHost
}

// EffectiveTargetURL reports the upstream URL Traefik is given on this node
// for the route, and whether it differs from the stored target. Only
// meaningful on the gateway node; elsewhere it echoes the stored target.
func (m *Materializer) EffectiveTargetURL(ctx context.Context, cfg gateway.Config, r gateway.Route) (url string, rewritten bool) {
	stored := targetURL(r)
	if !m.Status().IsGatewayNode || m.deps.Hosts == nil {
		return stored, false
	}
	h, err := m.deps.Hosts.GetHostByID(ctx, hosts.LocalCollectorHostID)
	if err != nil || h == nil {
		return stored, false
	}
	eff := targetURL(m.effectiveRoute(cfg, h, r))
	return eff, eff != stored
}

func targetURL(r gateway.Route) string {
	scheme := r.TargetScheme
	if scheme == "" {
		scheme = gateway.SchemeHTTP
	}
	return scheme + "://" + r.TargetHost + ":" + strconv.Itoa(r.TargetPort)
}

// Logs returns the managed Traefik's recent log output on the gateway node.
func (m *Materializer) Logs(ctx context.Context, tail int) (string, error) {
	if tail <= 0 || tail > 2000 {
		tail = 300
	}
	st := m.Status()
	if !st.IsGatewayNode {
		return "", errors.New("this node is not the gateway — open the gateway node's dashboard for Traefik logs")
	}
	if st.Mode != gateway.ModeManaged {
		return "", errors.New("external mode: Traefik is operated outside node-stats; check its own logs")
	}
	if m.native != nil {
		return m.native.Logs(ctx, tail)
	}
	if m.deps.DockerLogs == nil {
		return "", errors.New("docker log access is not available on this node")
	}
	return m.deps.DockerLogs(ctx, envOr("NODE_STATS_PROJECT", "node-stats"), tail)
}

// isLocalNode reports whether THIS node is the configured gateway — by the
// local host row's MAC or (Docker recreates change the NIC MAC) its stable
// system id / hardware UUID.
func (m *Materializer) isLocalNode(ctx context.Context, cfg gateway.Config) bool {
	if m.deps.Hosts == nil {
		return false
	}
	h, err := m.deps.Hosts.GetHostByID(ctx, hosts.LocalCollectorHostID)
	if err != nil || h == nil {
		return false
	}
	return cfg.IsNode(h.MacAddress, h.SystemHostID, h.HardwareUUID)
}

func (m *Materializer) localMAC(ctx context.Context) string {
	if m.deps.Hosts == nil {
		return ""
	}
	h, err := m.deps.Hosts.GetHostByID(ctx, hosts.LocalCollectorHostID)
	if err != nil || h == nil {
		return ""
	}
	return strings.ToLower(h.MacAddress)
}

func (m *Materializer) reconcile(ctx context.Context) {
	rc, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	st := Status{}
	defer func() {
		m.mu.Lock()
		m.status = st
		m.mu.Unlock()
	}()

	cfg, err := LoadConfig(rc, m.deps.Config)
	if err != nil {
		st.LastError = err.Error()
		return
	}
	isGW := cfg.Enabled && m.isLocalNode(rc, cfg)
	st.IsGatewayNode = isGW
	st.Mode = cfg.Mode

	// --- dynamic file ------------------------------------------------------
	if isGW {
		routes, err := m.deps.Repo.List(rc)
		if err != nil {
			st.LastError = err.Error()
			return
		}
		for _, r := range routes {
			if r.Enabled {
				st.RouteCount++
			}
		}
		dir := m.ManagedDynamicDir()
		if cfg.Mode == gateway.ModeExternal {
			dir = cfg.DynamicDir
		}
		path := filepath.Join(dir, gateway.DynamicFileName)
		st.FilePath = path
		content, err := gateway.Render(cfg, m.localizeTargets(rc, cfg, routes))
		if err != nil {
			st.LastError = "render: " + err.Error()
			return
		}
		if m.lastPath != "" && m.lastPath != path {
			m.removeOwned(m.lastPath)
		}
		if content == nil {
			// Nothing to serve: no file at all (Traefik rejects an empty http
			// section). Keep the dir so the managed container's bind mount holds.
			m.removeOwned(path)
			_ = os.MkdirAll(filepath.Dir(path), 0o755)
			now := time.Now().UTC()
			st.LastRenderAt = &now
			m.setLastPath(path)
		} else if err := writeIfChanged(path, content); err != nil {
			st.LastError = "write " + path + ": " + err.Error()
		} else {
			now := time.Now().UTC()
			st.LastRenderAt = &now
			m.setLastPath(path)
		}
	} else if m.lastPath != "" {
		// Gateway disabled or moved to another node — drop our file so the
		// (possibly still running) Traefik stops serving stale routes.
		m.removeOwned(m.lastPath)
		m.setLastPath("")
	}

	// --- managed backend: systemd (native root install) --------------------
	if m.native != nil {
		if isGW && cfg.Mode == gateway.ModeManaged {
			want := setup.GatewayProvision{Enabled: true, HTTPPort: cfg.HTTPPort, HTTPSPort: cfg.HTTPSPort,
				ACMEEnabled: cfg.ACMEEnabled, ACMEEmail: cfg.ACMEEmail, ACMEStaging: cfg.ACMEStaging}
			if restarted, err := m.native.Reconcile(rc, want); err != nil {
				st.LastError = "traefik (systemd): " + err.Error()
			} else if restarted && m.deps.Logger != nil {
				m.deps.Logger.Info("gateway: (re)started native traefik service")
			}
			healthy, detail := m.native.Health(rc)
			st.TraefikHealthy = &healthy
			st.TraefikDetail = detail
		} else if err := m.native.Disable(rc); err != nil {
			st.LastError = "traefik (systemd) disable: " + err.Error()
		}
		return
	}

	// Managed mode requested on a node that can drive no backend at all
	// (native non-root / no systemd): say so loudly instead of rendering into
	// a directory nothing reads.
	if isGW && cfg.Mode == gateway.ModeManaged && !setup.RunningInDocker() {
		_, why := nativeAvailable()
		st.LastError = "managed mode unavailable on this node: " + why + " — switch to external mode"
		return
	}

	// --- managed backend: docker controller (desired-state) ----------------
	if setup.RunningInDocker() && !setup.ManagedExternally() {
		want := setup.GatewayProvision{Enabled: isGW && cfg.Mode == gateway.ModeManaged}
		if want.Enabled {
			want.HTTPPort = cfg.HTTPPort
			want.HTTPSPort = cfg.HTTPSPort
			want.ACMEEnabled = cfg.ACMEEnabled
			want.ACMEEmail = cfg.ACMEEmail
			want.ACMEStaging = cfg.ACMEStaging
		}
		if want.Enabled {
			_ = os.MkdirAll(m.ManagedDynamicDir(), 0o755)
		}
		changed, err := setup.RequestGatewayState(m.deps.DataDir, m.deps.DBType, m.deps.DBDSN, want)
		if err != nil {
			st.LastError = "desired-state: " + err.Error()
		} else if changed && m.deps.Logger != nil {
			m.deps.Logger.Info("gateway: requested traefik state from controller", "enabled", want.Enabled)
		}
		if isGW && cfg.Mode == gateway.ModeManaged {
			if cs, err := setup.ReadControllerStatus(m.deps.DataDir); err == nil && cs != nil {
				st.Controller = cs
			}
			healthy := pingURL(rc, traefikPingURL)
			st.TraefikHealthy = &healthy
		}
	}
}

// writeIfChanged atomically writes content when the file differs (or is absent).
func writeIfChanged(path string, content []byte) error {
	if cur, err := os.ReadFile(path); err == nil && bytes.Equal(cur, content) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, content, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// removeOwned deletes a file only if it carries our generated-header marker —
// never an operator's file that happens to share the name.
func (m *Materializer) removeOwned(path string) {
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	if !bytes.HasPrefix(b, []byte("# Generated by node-stats (gateway)")) {
		return
	}
	if err := os.Remove(path); err == nil && m.deps.Logger != nil {
		m.deps.Logger.Info("gateway: removed dynamic file", "path", path)
	}
}

func (m *Materializer) lastPathFile() string {
	return filepath.Join(m.deps.DataDir, "gateway-last-path")
}

func (m *Materializer) readLastPath() string {
	b, err := os.ReadFile(m.lastPathFile())
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func (m *Materializer) setLastPath(p string) {
	if m.lastPath == p {
		return
	}
	m.lastPath = p
	if p == "" {
		_ = os.Remove(m.lastPathFile())
		return
	}
	_ = os.WriteFile(m.lastPathFile(), []byte(p+"\n"), 0o644)
}

func pingURL(ctx context.Context, url string) bool {
	rc, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(rc, http.MethodGet, url, nil)
	if err != nil {
		return false
	}
	resp, err := pingClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

var pingClient = &http.Client{
	Timeout:   3 * time.Second,
	Transport: &http.Transport{DisableKeepAlives: true, MaxIdleConns: 1, IdleConnTimeout: 5 * time.Second},
}
