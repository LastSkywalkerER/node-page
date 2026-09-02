package docker

import (
	"context"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/log"
	"github.com/docker/docker/api/types/container"
	yaml "gopkg.in/yaml.v2"
)

// traefikRoute is one resolved reverse-proxy route: a public host rule pointing
// at an upstream service:port. It is provider-generic — populated from both the
// Traefik file provider (dokploy, plain Traefik) and nginx / Nginx Proxy
// Manager configs (see nginx.go).
type traefikRoute struct {
	Host         string // public hostname from the Host(`…`) rule / server_name
	Scheme       string // http | https
	UpstreamHost string // service/container the router forwards to (tasks. stripped)
	// PublicPort is the entrypoint's published port when known (node-stats
	// gateway header hint); 0 = default for the scheme.
	PublicPort   int
	UpstreamPort int // upstream port: the container's INTERNAL port for a named
	// upstream, or the host's PUBLISHED port when UpstreamIsIP (NPM "IP:port").
	// UpstreamIsIP marks an upstream addressed by IP literal rather than a
	// service/container name (the NPM "forward to 192.168.1.5:8080" case): it is
	// matched to the container that PUBLISHES UpstreamPort, not by name.
	UpstreamIsIP bool
}

// URL returns the external URL for the route, or "" when no host is known.
func (r traefikRoute) URL() string {
	if r.Host == "" {
		return ""
	}
	scheme := r.Scheme
	if scheme == "" {
		scheme = "http"
	}
	u := scheme + "://" + r.Host
	if r.PublicPort > 0 && !((scheme == "http" && r.PublicPort == 80) || (scheme == "https" && r.PublicPort == 443)) {
		u += ":" + strconv.Itoa(r.PublicPort)
	}
	return u
}

// ProxyRoute is the exported name of a resolved reverse-proxy route, for
// external route sources (the cluster gateway's replicated route table).
type ProxyRoute = traefikRoute

// ProxyRouteSource yields extra routes to attach during collection.
type ProxyRouteSource func(ctx context.Context) []ProxyRoute

var (
	proxyRouteSourceMu sync.RWMutex
	proxyRouteSourceFn ProxyRouteSource
)

// RegisterProxyRouteSource installs the gateway route source (wired once at
// startup in server.go; the collector is constructed inside DI without access
// to the gateway layer, hence a package-level hook).
func RegisterProxyRouteSource(fn ProxyRouteSource) {
	proxyRouteSourceMu.Lock()
	defer proxyRouteSourceMu.Unlock()
	proxyRouteSourceFn = fn
}

func proxyRouteSource() ProxyRouteSource {
	proxyRouteSourceMu.RLock()
	defer proxyRouteSourceMu.RUnlock()
	return proxyRouteSourceFn
}

// gatewayPortHint matches the header the node-stats gateway renderer writes so
// this parser can build public URLs with the gateway's non-default ports.
var gatewayPortHint = regexp.MustCompile(`(?m)^# node-stats-gateway: http_port=(\d+) https_port=(\d+)`)

// gatewayPorts extracts the (http, https) public ports from a generated file
// header; zeros when the file isn't ours.
func gatewayPorts(data []byte) (int, int) {
	m := gatewayPortHint.FindSubmatch(data)
	if m == nil {
		return 0, 0
	}
	h, _ := strconv.Atoi(string(m[1]))
	s, _ := strconv.Atoi(string(m[2]))
	return h, s
}

// defaultTraefikDirs are well-known Traefik file-provider dynamic-config
// locations probed when TRAEFIK_DYNAMIC_DIR is unset. Standard Traefik paths
// plus the dokploy default — all overridable via the env var, so we work for
// any file-provider setup without being tied to one platform.
func defaultTraefikDirs() []string {
	return []string{
		"/etc/dokploy/traefik/dynamic",
		"/etc/traefik/dynamic",
		"/etc/traefik/conf.d",
		"/data/traefik/dynamic",
	}
}

// Traefik dynamic-config document subset we care about.
type traefikRouterSpec struct {
	Rule        string          `yaml:"rule"`
	Service     string          `yaml:"service"`
	EntryPoints []string        `yaml:"entryPoints"`
	TLS         *traefikTLSSpec `yaml:"tls"`
}

type traefikTLSSpec struct {
	CertResolver string `yaml:"certResolver"`
}

type traefikServiceSpec struct {
	LoadBalancer struct {
		Servers []struct {
			URL string `yaml:"url"`
		} `yaml:"servers"`
	} `yaml:"loadBalancer"`
}

type traefikDoc struct {
	HTTP struct {
		Routers  map[string]traefikRouterSpec  `yaml:"routers"`
		Services map[string]traefikServiceSpec `yaml:"services"`
	} `yaml:"http"`
}

// traefikRouteCache memoises parsed routes for a short TTL so the 5s
// collection cycle doesn't re-read/re-parse the dynamic dir every tick.
var traefikRouteCache struct {
	mu        sync.Mutex
	routes    []traefikRoute
	expiresAt time.Time
}

const traefikCacheTTL = 30 * time.Second

// loadTraefikRoutes returns the resolved routes from the given dirs (falling
// back to the well-known defaults when empty), memoised for traefikCacheTTL.
func loadTraefikRoutes(dirs []string) []traefikRoute {
	if len(dirs) == 0 {
		dirs = defaultTraefikDirs()
	} else {
		dirs = append(dirs, defaultTraefikDirs()...)
	}
	traefikRouteCache.mu.Lock()
	defer traefikRouteCache.mu.Unlock()
	if !traefikRouteCache.expiresAt.IsZero() && time.Now().Before(traefikRouteCache.expiresAt) {
		return traefikRouteCache.routes
	}
	routes := parseTraefikDirs(dirs)
	traefikRouteCache.routes = routes
	traefikRouteCache.expiresAt = time.Now().Add(traefikCacheTTL)
	return routes
}

// expandTraefikDirs dedupes the candidate dirs and adds a HOST_ROOT-prefixed
// variant for each absolute path, so a containerised node-stats with the host
// root bind-mounted (e.g. /host) can read configs at their host locations
// without per-dir bind mounts.
func expandTraefikDirs(dirs []string) []string {
	root := hostComposeRoot()
	seen := map[string]struct{}{}
	out := make([]string, 0, len(dirs)*2)
	add := func(d string) {
		d = filepath.Clean(strings.TrimSpace(d))
		if d == "" || d == "." || d == "/" {
			return
		}
		if _, ok := seen[d]; ok {
			return
		}
		seen[d] = struct{}{}
		out = append(out, d)
	}
	for _, d := range dirs {
		add(d)
		if root != "" && filepath.IsAbs(d) && !strings.HasPrefix(d, root+string(filepath.Separator)) {
			add(filepath.Join(root, d))
		}
	}
	return out
}

// parseTraefikDirs reads every *.yml/*.yaml document under the given dirs,
// merges all routers + services (so cross-file references resolve), and
// returns the resolved routes. Missing/unreadable dirs are skipped silently.
func parseTraefikDirs(dirs []string) []traefikRoute {
	routers := map[string]traefikRouterSpec{}
	services := map[string]traefikServiceSpec{}
	routerPorts := map[string][2]int{} // router → (http, https) public ports from a gateway header
	dirHasACME := false

	dirs = expandTraefikDirs(dirs)
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() {
				continue
			}
			if name == "acme.json" {
				dirHasACME = true
				continue
			}
			if !strings.HasSuffix(name, ".yml") && !strings.HasSuffix(name, ".yaml") {
				continue
			}
			data, err := os.ReadFile(filepath.Join(dir, name)) // #nosec G304 — operator-configured config dir, not user input
			if err != nil {
				continue
			}
			var doc traefikDoc
			if yaml.Unmarshal(data, &doc) != nil {
				continue
			}
			httpPort, httpsPort := gatewayPorts(data)
			for k, v := range doc.HTTP.Routers {
				routers[k] = v
				if httpPort > 0 || httpsPort > 0 {
					routerPorts[k] = [2]int{httpPort, httpsPort}
				}
			}
			for k, v := range doc.HTTP.Services {
				services[k] = v
			}
		}
	}

	var out []traefikRoute
	for name, rt := range routers {
		hosts := hostsFromRule(rt.Rule)
		if len(hosts) == 0 {
			continue
		}
		upstreamHost, upstreamPort := "", 0
		if svc, ok := services[rt.Service]; ok {
			for _, s := range svc.LoadBalancer.Servers {
				if h, p, ok := parseUpstreamURL(s.URL); ok {
					upstreamHost, upstreamPort = h, p
					break
				}
			}
		}
		// One route per hostname — a single router can carry several domains
		// (Host(`a`) || Host(`b`), or Host(`a`, `b`)) and none may be dropped.
		for _, host := range hosts {
			r := traefikRoute{
				Host:         host,
				Scheme:       traefikScheme(rt.EntryPoints, rt.TLS != nil, dirHasACME, host),
				UpstreamHost: upstreamHost,
				UpstreamPort: upstreamPort,
			}
			if pp, ok := routerPorts[name]; ok {
				if r.Scheme == "https" {
					r.PublicPort = pp[1]
				} else {
					r.PublicPort = pp[0]
				}
			}
			out = append(out, r)
		}
	}
	return out
}

var (
	// traefikHostCall matches every Host(...) call in a router rule.
	traefikHostCall = regexp.MustCompile(`(?i)Host\(([^)]*)\)`)
	// traefikBacktickArg extracts each backtick-quoted argument inside a call.
	traefikBacktickArg = regexp.MustCompile("`([^`]+)`")
)

// hostsFromRule extracts EVERY hostname from a Traefik router rule — across
// multiple Host() calls (`Host(`a`) || Host(`b`)`) and comma-separated args
// (`Host(`a`, `b`)`). Wildcards are skipped.
func hostsFromRule(rule string) []string {
	var out []string
	seen := map[string]struct{}{}
	for _, call := range traefikHostCall.FindAllStringSubmatch(rule, -1) {
		for _, arg := range traefikBacktickArg.FindAllStringSubmatch(call[1], -1) {
			h := strings.TrimSpace(arg[1])
			if h == "" || strings.Contains(h, "*") {
				continue
			}
			if _, ok := seen[h]; ok {
				continue
			}
			seen[h] = struct{}{}
			out = append(out, h)
		}
	}
	return out
}

// parseUpstreamURL splits a Traefik server URL ("http://tasks.web:3000") into
// the upstream host (Swarm "tasks." prefix stripped) and port.
func parseUpstreamURL(raw string) (host string, port int, ok bool) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Hostname() == "" {
		return "", 0, false
	}
	host = strings.TrimPrefix(u.Hostname(), "tasks.")
	if p := u.Port(); p != "" {
		port, _ = strconv.Atoi(p)
	}
	return host, port, host != ""
}

// traefikScheme decides http vs https: an explicit websecure/https entrypoint
// or a tls block wins; otherwise, when the dynamic dir manages ACME certs and
// the host is a FQDN, we assume https (the common reverse-proxy case).
func traefikScheme(entryPoints []string, hasTLS, dirHasACME bool, host string) string {
	for _, e := range entryPoints {
		switch strings.ToLower(strings.TrimSpace(e)) {
		case "websecure", "https":
			return "https"
		}
	}
	if hasTLS {
		return "https"
	}
	if dirHasACME && strings.Contains(host, ".") {
		return "https"
	}
	return "http"
}

// enrichWithProxyRoutes attaches reverse-proxy-derived public URLs (Traefik or
// nginx/NPM) to each container's ports. A route whose upstream port isn't an
// already-known port is appended as a synthetic (unpublished) port carrying the
// URL, so the UI can surface "reachable at <url>" even when the container
// publishes nothing.
func enrichWithProxyRoutes(m *DockerMetric, routes []traefikRoute, logger *log.Logger) {
	if m == nil || len(routes) == 0 {
		return
	}
	// Flatten all containers once so a route can be resolved against the whole
	// host (it may target any project's container, and replicas of one service
	// span multiple container rows).
	var all []*DockerContainer
	for si := range m.Stacks {
		for ci := range m.Stacks[si].Containers {
			all = append(all, &m.Stacks[si].Containers[ci])
		}
	}

	// Routes come out of YAML/nginx maps in RANDOM order. Attachment order
	// decides which domain lands on the container's real published-port row
	// (and so what the app's public_url / icon favicon origin becomes) — sort
	// best-first so the result is identical on every collection tick instead
	// of flapping "https://localhost" ↔ "https://dashboard.example.com".
	routes = sortRoutesByPreference(routes)

	for _, r := range routes {
		if r.URL() == "" {
			continue
		}
		var matches []*DockerContainer
		projects := map[string]struct{}{}
		for _, c := range all {
			if routeMatchesContainer(r, c) {
				matches = append(matches, c)
				projects[c.Project] = struct{}{}
			}
		}
		// A bare upstream host (e.g. "web") can collide with an unrelated
		// container/project sharing that name. When matches span more than one
		// project we can't tell which is intended, so skip rather than mislabel.
		// Replicas of a single service all share one project and still match.
		if len(matches) == 0 || len(projects) > 1 {
			// Surface WHY a domain didn't land on any app (run with DEBUG=true):
			// either no container matched the upstream name, or it was ambiguous.
			if logger != nil {
				logger.Debug("Traefik route not attached",
					"host", r.Host, "upstream", r.UpstreamHost, "port", r.UpstreamPort,
					"matched_containers", len(matches), "matched_projects", len(projects))
			}
			continue
		}
		for _, c := range matches {
			attachRouteURL(c, r)
		}
	}
}

// routeMatchesContainer reports whether a route's upstream names this
// container. Named upstreams (Traefik services, NPM forward-hostnames) match
// across compose/swarm naming conventions; an IP-literal upstream (NPM "forward
// to 192.168.1.5:8080") matches the container that PUBLISHES that host port.
func routeMatchesContainer(r traefikRoute, c *DockerContainer) bool {
	h := r.UpstreamHost
	if h == "" {
		return false
	}
	if r.UpstreamIsIP || upstreamIsThisHost(h) {
		// A host port is bound by at most one container, so matching by published
		// port is unambiguous. Port 0 (unknown) can't be resolved → no match.
		if r.UpstreamPort == 0 {
			return false
		}
		for _, p := range c.Ports {
			if p.PublicPort == r.UpstreamPort {
				return true
			}
		}
		return false
	}
	if h == c.Project || h == c.Service || h == c.Name {
		return true
	}
	// Swarm task name "<service>.<slot>.<taskid>" starts with the service.
	if strings.HasPrefix(c.Name, h+".") {
		return true
	}
	// Compose network alias "<project>-<service>" / "<project>_<service>".
	if c.Project != "" && c.Service != "" {
		if h == c.Project+"-"+c.Service || h == c.Project+"_"+c.Service {
			return true
		}
	}
	return false
}

// attachRouteURL records the route's URL on the matching private port. When the
// target port isn't listed — or already carries a DIFFERENT domain (several
// routers can front one upstream, e.g. two domains → one service:3000) — a
// synthetic unpublished port row is appended so no domain is ever dropped.
// upstreamIsThisHost reports whether an upstream hostname denotes the docker
// host itself (the gateway renders same-host targets as host.docker.internal),
// in which case the port is a PUBLISHED host port like for an IP literal.
func upstreamIsThisHost(h string) bool {
	switch strings.ToLower(h) {
	case "host.docker.internal", "localhost", "127.0.0.1", "gateway.docker.internal":
		return true
	}
	return false
}

func attachRouteURL(c *DockerContainer, r traefikRoute) {
	u := r.URL()
	for i := range c.Ports {
		if c.Ports[i].PublicURL == u {
			return // this domain is already attached
		}
	}
	for i := range c.Ports {
		// Named upstreams target the container's INTERNAL (private) port; an
		// IP-literal upstream targets the PUBLISHED (host) port.
		portMatch := r.UpstreamPort == 0
		if !portMatch {
			if r.UpstreamIsIP || upstreamIsThisHost(r.UpstreamHost) {
				portMatch = c.Ports[i].PublicPort == r.UpstreamPort
			} else {
				portMatch = c.Ports[i].PrivatePort == r.UpstreamPort
			}
		}
		if portMatch {
			if c.Ports[i].PublicURL == "" {
				c.Ports[i].PublicURL = u
				return
			}
			// Slot taken by another domain — keep scanning for a free row on the
			// same port (an earlier synthetic append may have created one).
		}
	}
	c.Ports = append(c.Ports, DockerPort{PrivatePort: r.UpstreamPort, Type: "tcp", PublicURL: u})
}

// sortRoutesByPreference returns a copy of routes ordered best-first (see
// publicURLRank), ties broken by URL so the order is fully deterministic.
func sortRoutesByPreference(routes []traefikRoute) []traefikRoute {
	out := make([]traefikRoute, len(routes))
	copy(out, routes)
	sort.SliceStable(out, func(i, j int) bool {
		ui, uj := out[i].URL(), out[j].URL()
		ri, rj := publicURLRank(ui), publicURLRank(uj)
		if ri != rj {
			return ri < rj
		}
		return ui < uj
	})
	return out
}

// publicURLRank orders candidate public URLs for one app (lower = better): a
// real https domain beats http, and any domain beats a loopback / .local /
// bare-IP address that only means something on the machine itself. Equal
// ranks fall back to a plain string compare in the callers.
func publicURLRank(raw string) int {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return 100
	}
	h := strings.ToLower(u.Hostname())
	local := upstreamIsThisHost(h) || strings.HasSuffix(h, ".local") ||
		strings.HasSuffix(h, ".localhost") || strings.HasSuffix(h, ".internal") || net.ParseIP(h) != nil
	dotted := strings.Contains(h, ".")
	rank := 0
	if local {
		rank += 40
	} else if !dotted {
		rank += 20 // bare single-label name — resolvable only on a LAN
	}
	if u.Scheme != "https" {
		rank += 10
	}
	return rank
}

// firstContainerPortURL returns the best proxy URL attached to any of the
// container's ports (publicURLRank, then lexical — never Docker's port order,
// which is not stable across ticks), used as the app-level public link when no
// label provided one.
func firstContainerPortURL(c DockerContainer) string {
	best, bestRank := "", 0
	for _, p := range c.Ports {
		if p.PublicURL == "" {
			continue
		}
		r := publicURLRank(p.PublicURL)
		if best == "" || r < bestRank || (r == bestRank && p.PublicURL < best) {
			best, bestRank = p.PublicURL, r
		}
	}
	return best
}

// traefikDirsFromStaticConfig reads a Traefik STATIC config (traefik.yml) at a
// host path (tried directly, then under HOST_ROOT) and returns the file
// provider's directory/filename mapped from container paths to host paths via
// the proxy container's mounts. This is how dokploy wires its file provider —
// in the static config, not CLI args. Best-effort.
func traefikDirsFromStaticConfig(hostPath string, mounts []container.MountPoint) []string {
	data, ok := readHostFile(hostPath)
	if !ok {
		return nil
	}
	var cfg struct {
		Providers struct {
			File struct {
				Directory string `yaml:"directory"`
				Filename  string `yaml:"filename"`
			} `yaml:"file"`
		} `yaml:"providers"`
	}
	if yaml.Unmarshal(data, &cfg) != nil {
		return nil
	}
	var out []string
	if d := strings.TrimSpace(cfg.Providers.File.Directory); d != "" {
		out = append(out, containerPathToHost(d, mounts))
	}
	if f := strings.TrimSpace(cfg.Providers.File.Filename); f != "" {
		out = append(out, containerPathToHost(filepath.Dir(f), mounts))
	}
	return out
}

// readHostFile reads a host-path file directly or under the HOST_ROOT bind-mount.
func readHostFile(p string) ([]byte, bool) {
	if b, err := os.ReadFile(p); err == nil { // #nosec G304 — path derived from container mounts/args, not user input
		return b, true
	}
	if root := hostComposeRoot(); root != "" && filepath.IsAbs(p) {
		if b, err := os.ReadFile(filepath.Join(root, p)); err == nil { // #nosec G304
			return b, true
		}
	}
	return nil, false
}

// ---- Domain-detection diagnostic (GET /docker/traefik-discovery) ----

// TraefikDirStatus describes one candidate dynamic-config dir in the report.
type TraefikDirStatus struct {
	Path      string `json:"path"`
	Readable  bool   `json:"readable"`
	YAMLFiles int    `json:"yaml_files"`
}

// TraefikRouteStatus is one parsed route and its attachment outcome.
type TraefikRouteStatus struct {
	Host              string   `json:"host"`
	URL               string   `json:"url"`
	UpstreamHost      string   `json:"upstream_host"`
	UpstreamPort      int      `json:"upstream_port"`
	MatchedContainers []string `json:"matched_containers,omitempty"`
	MatchedProjects   []string `json:"matched_projects,omitempty"`
	Attached          bool     `json:"attached"`
	Reason            string   `json:"reason,omitempty"`
}

// TraefikDiscoveryReport explains the whole domain-detection pipeline for this
// node: which dirs were configured/discovered and are actually readable, which
// routes parsed out of them, and why each route did or didn't land on an app.
type TraefikDiscoveryReport struct {
	EnvDirs         []string             `json:"env_dirs,omitempty"`
	DiscoveredDirs  []string             `json:"discovered_dirs,omitempty"`
	DefaultDirs     []string             `json:"default_dirs"`
	HostRoot        string               `json:"host_root,omitempty"`
	ProxyCandidates []string             `json:"proxy_candidates,omitempty"`
	Dirs            []TraefikDirStatus   `json:"dirs"`
	Routes          []TraefikRouteStatus `json:"routes"`

	// nginx / Nginx Proxy Manager equivalents (NGINX_DYNAMIC_DIR + discovery).
	NginxEnvDirs        []string             `json:"nginx_env_dirs,omitempty"`
	NginxDiscoveredDirs []string             `json:"nginx_discovered_dirs,omitempty"`
	NginxDefaultDirs    []string             `json:"nginx_default_dirs"`
	NginxDirs           []TraefikDirStatus   `json:"nginx_dirs"`
	NginxRoutes         []TraefikRouteStatus `json:"nginx_routes"`
}

// TraefikDiscoveryReport runs a FRESH discovery (no caches) and simulates route
// attachment against the live container list, so an operator can see exactly
// where domain detection breaks on their layout.
func (c *dockerMetricsCollector) TraefikDiscoveryReport(ctx context.Context) TraefikDiscoveryReport {
	rep := TraefikDiscoveryReport{
		EnvDirs:          c.traefikDirs,
		DefaultDirs:      defaultTraefikDirs(),
		HostRoot:         hostComposeRoot(),
		NginxEnvDirs:     c.nginxDirs,
		NginxDefaultDirs: defaultNginxDirs(),
	}
	if c.client == nil || !c.IsDockerAvailable(ctx) {
		return rep
	}
	containers, err := c.client.ContainerList(ctx, container.ListOptions{All: true})
	if err != nil {
		return rep
	}

	for _, ci := range containers {
		if !isProxyCandidate(ci) {
			continue
		}
		name := ci.Image
		if len(ci.Names) > 0 {
			name = strings.TrimPrefix(ci.Names[0], "/") + " (" + ci.Image + ")"
		}
		rep.ProxyCandidates = append(rep.ProxyCandidates, name)
		rep.DiscoveredDirs = append(rep.DiscoveredDirs, c.introspectProxyContainer(ctx, ci.ID)...)
	}

	// nginx / NPM config dirs from container introspection (mirrors discoverNginxDirs).
	for _, ci := range containers {
		if !isNginxContainer(ci) && !isProxyCandidate(ci) {
			continue
		}
		rep.NginxDiscoveredDirs = append(rep.NginxDiscoveredDirs, c.introspectNginxContainer(ctx, ci.ID)...)
	}

	all := expandTraefikDirs(append(append(append([]string{}, c.traefikDirs...), rep.DiscoveredDirs...), defaultTraefikDirs()...))
	for _, d := range all {
		st := TraefikDirStatus{Path: d}
		if entries, err := os.ReadDir(d); err == nil {
			st.Readable = true
			for _, e := range entries {
				if !e.IsDir() && (strings.HasSuffix(e.Name(), ".yml") || strings.HasSuffix(e.Name(), ".yaml")) {
					st.YAMLFiles++
				}
			}
		}
		rep.Dirs = append(rep.Dirs, st)
	}

	nginxAll := expandTraefikDirs(append(append(append([]string{}, c.nginxDirs...), rep.NginxDiscoveredDirs...), defaultNginxDirs()...))
	for _, d := range nginxAll {
		st := TraefikDirStatus{Path: d}
		if entries, err := os.ReadDir(d); err == nil {
			st.Readable = true
			for _, e := range entries {
				n := e.Name()
				if e.IsDir() || strings.HasPrefix(n, ".") {
					continue
				}
				if ext := filepath.Ext(n); ext == "" || ext == ".conf" {
					st.YAMLFiles++ // config-file count (nginx uses .conf, not yaml)
				}
			}
		}
		rep.NginxDirs = append(rep.NginxDirs, st)
	}

	// Light containers carry exactly what routeMatchesContainer consults (names
	// + published ports, the latter for nginx IP-literal upstream matching).
	light := make([]DockerContainer, 0, len(containers))
	for _, ci := range containers {
		name := ""
		if len(ci.Names) > 0 {
			name = strings.TrimPrefix(ci.Names[0], "/")
		}
		project, svc, _ := resolveProject(ci.Labels, name)
		dc := DockerContainer{Name: name, Project: project, Service: svc, Labels: ci.Labels}
		for _, p := range ci.Ports {
			dc.Ports = append(dc.Ports, DockerPort{PrivatePort: int(p.PrivatePort), PublicPort: int(p.PublicPort), Type: p.Type})
		}
		light = append(light, dc)
	}

	rep.Routes = simulateRouteAttachment(parseTraefikDirs(all), light)
	// nginx routes from both the host-path scan and a direct read of the proxy
	// container's filesystem (the latter works when /host can't see the config).
	nginxRoutes := append(parseNginxDirs(nginxAll), c.nginxRoutesFromContainers(ctx, containers)...)
	rep.NginxRoutes = simulateRouteAttachment(nginxRoutes, light)
	return rep
}

// simulateRouteAttachment replays route→container matching (no mutation) for the
// discovery report, explaining why each route did or didn't land on an app.
func simulateRouteAttachment(routes []traefikRoute, light []DockerContainer) []TraefikRouteStatus {
	var out []TraefikRouteStatus
	for _, r := range routes {
		st := TraefikRouteStatus{
			Host: r.Host, URL: r.URL(),
			UpstreamHost: r.UpstreamHost, UpstreamPort: r.UpstreamPort,
		}
		projects := map[string]struct{}{}
		for i := range light {
			if routeMatchesContainer(r, &light[i]) {
				st.MatchedContainers = append(st.MatchedContainers, light[i].Name)
				projects[light[i].Project] = struct{}{}
			}
		}
		for p := range projects {
			st.MatchedProjects = append(st.MatchedProjects, p)
		}
		sort.Strings(st.MatchedProjects)
		switch {
		case r.UpstreamHost == "":
			st.Reason = "no upstream resolved from config"
		case len(st.MatchedContainers) == 0:
			st.Reason = "no container matches the upstream host"
		case len(projects) > 1:
			st.Reason = "ambiguous: upstream matches several projects"
		default:
			st.Attached = true
		}
		out = append(out, st)
	}
	return out
}
