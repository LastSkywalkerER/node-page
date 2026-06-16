package docker

import (
	"context"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/docker/docker/api/types/container"
)

// This file mirrors traefik.go for nginx / Nginx Proxy Manager (NPM): it parses
// nginx `server { … }` configs (NPM's generated proxy_host/*.conf and generic
// nginx conf.d/sites-enabled) into the provider-generic traefikRoute, which the
// shared matchers (routeMatchesContainer / attachRouteURL) then attach to app
// container ports — exactly like Traefik file-provider routes.

// defaultNginxDirs are well-known nginx / NPM config locations probed when
// NGINX_DYNAMIC_DIR is unset. NPM generates one proxy_host/<id>.conf per host.
func defaultNginxDirs() []string {
	return []string{
		"/data/nginx/proxy_host",         // NPM inside its own container
		"/opt/npm/data/nginx/proxy_host", // NPM common host bind-mount
		"/etc/nginx/conf.d",              // generic nginx
		"/etc/nginx/sites-enabled",       // generic nginx (debian layout)
	}
}

// nginxRouteCache memoises parsed routes for traefikCacheTTL so the 5s
// collection cycle doesn't re-read/re-parse the config dirs every tick.
var nginxRouteCache struct {
	mu        sync.Mutex
	routes    []traefikRoute
	expiresAt time.Time
}

// loadNginxRoutes returns the resolved routes from the given dirs (falling back
// to the well-known defaults), memoised for traefikCacheTTL.
func loadNginxRoutes(dirs []string) []traefikRoute {
	if len(dirs) == 0 {
		dirs = defaultNginxDirs()
	} else {
		dirs = append(dirs, defaultNginxDirs()...)
	}
	nginxRouteCache.mu.Lock()
	defer nginxRouteCache.mu.Unlock()
	if !nginxRouteCache.expiresAt.IsZero() && time.Now().Before(nginxRouteCache.expiresAt) {
		return nginxRouteCache.routes
	}
	routes := parseNginxDirs(dirs)
	nginxRouteCache.routes = routes
	nginxRouteCache.expiresAt = time.Now().Add(traefikCacheTTL)
	return routes
}

// parseNginxDirs reads every *.conf (and extension-less) file under the given
// dirs (HOST_ROOT variants added via expandTraefikDirs) and returns the
// resolved routes. Missing/unreadable dirs are skipped silently.
func parseNginxDirs(dirs []string) []traefikRoute {
	dirs = expandTraefikDirs(dirs)
	var out []traefikRoute
	seen := map[string]struct{}{}
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			name := e.Name()
			if strings.HasPrefix(name, ".") {
				continue
			}
			// .conf files (NPM, conf.d) or extension-less (sites-enabled).
			if ext := filepath.Ext(name); ext != "" && ext != ".conf" {
				continue
			}
			data, err := os.ReadFile(filepath.Join(dir, name)) // #nosec G304 — operator-configured config dir, not user input
			if err != nil {
				continue
			}
			for _, r := range routesFromNginxConf(string(data)) {
				key := r.Host + "\x00" + r.URL()
				if _, ok := seen[key]; ok {
					continue
				}
				seen[key] = struct{}{}
				out = append(out, r)
			}
		}
	}
	return out
}

var nginxServerOpen = regexp.MustCompile(`(?s)(^|[;{}\s])server\s*\{`)

// routesFromNginxConf extracts one route per server_name host from every
// `server { … }` block in a config. When several blocks serve the same host
// (NPM emits an http :80 redirect block + an https :443 proxy block) the one
// that actually proxies wins, then https over http.
func routesFromNginxConf(conf string) []traefikRoute {
	type cand struct {
		scheme       string
		upstreamHost string
		upstreamPort int
		upstreamIsIP bool
		hasUpstream  bool
	}
	best := map[string]cand{}
	var order []string

	for _, block := range nginxServerBlocks(conf) {
		hostnames := nginxServerNames(block)
		if len(hostnames) == 0 {
			continue
		}
		scheme := nginxListenScheme(block)
		uh, up, isIP, ok := nginxUpstream(block)
		c := cand{scheme: scheme, upstreamHost: uh, upstreamPort: up, upstreamIsIP: isIP, hasUpstream: ok}
		for _, h := range hostnames {
			prev, seen := best[h]
			if !seen {
				best[h] = c
				order = append(order, h)
				continue
			}
			better := (c.hasUpstream && !prev.hasUpstream) ||
				(c.hasUpstream == prev.hasUpstream && c.scheme == "https" && prev.scheme != "https")
			if better {
				best[h] = c
			}
		}
	}

	out := make([]traefikRoute, 0, len(order))
	for _, h := range order {
		c := best[h]
		out = append(out, traefikRoute{
			Host:         h,
			Scheme:       c.scheme,
			UpstreamHost: c.upstreamHost,
			UpstreamPort: c.upstreamPort,
			UpstreamIsIP: c.upstreamIsIP,
		})
	}
	return out
}

// nginxServerBlocks returns the inner text of every top-level `server { … }`
// block (brace-balanced, so nested location blocks are included). Comments are
// stripped first so a commented-out brace can't unbalance the scan.
func nginxServerBlocks(conf string) []string {
	conf = stripNginxComments(conf)
	var blocks []string
	i := 0
	for i < len(conf) {
		loc := nginxServerOpen.FindStringIndex(conf[i:])
		if loc == nil {
			break
		}
		start := i + loc[1] // position just after the '{'
		depth, j := 1, start
		for j < len(conf) && depth > 0 {
			switch conf[j] {
			case '{':
				depth++
			case '}':
				depth--
			}
			j++
		}
		if depth != 0 {
			break // unbalanced — bail rather than misparse
		}
		blocks = append(blocks, conf[start:j-1])
		i = j
	}
	return blocks
}

// stripNginxComments removes `# …` line comments. nginx directive values
// (server_name, proxy_pass) don't contain '#', so a line-based strip is safe.
func stripNginxComments(conf string) string {
	var b strings.Builder
	b.Grow(len(conf))
	for _, line := range strings.Split(conf, "\n") {
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}

var (
	nginxServerNameRe = regexp.MustCompile(`(?m)\bserver_name\s+([^;]+);`)
	nginxListenRe     = regexp.MustCompile(`(?m)\blisten\s+([^;]+);`)
	nginxProxyPassRe  = regexp.MustCompile(`(?m)\bproxy_pass\s+([^;]+);`)
	nginxSetRe        = regexp.MustCompile(`(?m)\bset\s+\$(\w+)\s+([^;]+);`)
	nginxVarRe        = regexp.MustCompile(`\$\{?(\w+)\}?`)
)

// nginxServerNames extracts the public hostnames from a server block's
// server_name directive(s), skipping the catch-all `_`, wildcards and the
// default_server token.
func nginxServerNames(block string) []string {
	var out []string
	seen := map[string]struct{}{}
	for _, m := range nginxServerNameRe.FindAllStringSubmatch(block, -1) {
		for _, tok := range strings.Fields(m[1]) {
			h := strings.Trim(strings.TrimSpace(tok), `"'`)
			if h == "" || h == "_" || strings.Contains(h, "*") || strings.EqualFold(h, "default_server") {
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

// nginxListenScheme reports https when any listen directive carries ssl/443,
// else http.
func nginxListenScheme(block string) string {
	for _, m := range nginxListenRe.FindAllStringSubmatch(block, -1) {
		v := strings.ToLower(m[1])
		if strings.Contains(v, "ssl") || strings.Contains(v, "443") {
			return "https"
		}
	}
	return "http"
}

// nginxUpstream resolves the backend host:port from a server block's first
// proxy_pass, substituting NPM's `set $forward_scheme/$server/$port` variables.
// ok is false when the block has no resolvable proxy_pass (e.g. a pure
// redirect). isIP marks an IP-literal backend (matched by published port).
func nginxUpstream(block string) (host string, port int, isIP bool, ok bool) {
	pp := nginxProxyPassRe.FindStringSubmatch(block)
	if pp == nil {
		return "", 0, false, false
	}
	raw := strings.TrimSpace(pp[1])

	if strings.Contains(raw, "$") {
		vars := map[string]string{}
		for _, m := range nginxSetRe.FindAllStringSubmatch(block, -1) {
			vars[m[1]] = strings.Trim(strings.TrimSpace(m[2]), `"'`)
		}
		raw = nginxVarRe.ReplaceAllStringFunc(raw, func(tok string) string {
			name := nginxVarRe.FindStringSubmatch(tok)[1]
			if v, ok := vars[name]; ok {
				return v
			}
			return tok
		})
	}

	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || strings.Contains(u.Hostname(), "$") {
		return "", 0, false, false
	}
	host = strings.TrimPrefix(u.Hostname(), "tasks.")
	if p := u.Port(); p != "" {
		port, _ = strconv.Atoi(p)
	}
	return host, port, net.ParseIP(host) != nil, true
}

// ---- container introspection ----

// isNginxContainer reports whether a container looks like nginx or Nginx Proxy
// Manager (by image or name).
func isNginxContainer(ci container.Summary) bool {
	hay := strings.ToLower(ci.Image)
	for _, n := range ci.Names {
		hay += " " + strings.ToLower(strings.TrimPrefix(n, "/"))
	}
	return strings.Contains(hay, "nginx") || strings.Contains(hay, "npm") || strings.Contains(hay, "proxy-manager")
}

// discoverNginxDirs introspects running nginx / NPM containers (or whatever
// fronts 80/443) to locate their config dirs on the host, so domain discovery
// works without an env var. Refreshed on the slow traefikDiscoveryInterval.
func (c *dockerMetricsCollector) discoverNginxDirs(ctx context.Context, containers []container.Summary) []string {
	c.cacheMutex.RLock()
	fresh := !c.nginxDiscoveryAt.IsZero() && time.Since(c.nginxDiscoveryAt) < traefikDiscoveryInterval
	cached := c.discoveredNginxDirs
	c.cacheMutex.RUnlock()
	if fresh || c.client == nil {
		return cached
	}

	var found []string
	seen := map[string]struct{}{}
	add := func(p string) {
		p = filepath.Clean(strings.TrimSpace(p))
		if p == "" || p == "." || p == "/" {
			return
		}
		if _, ok := seen[p]; ok {
			return
		}
		seen[p] = struct{}{}
		found = append(found, p)
	}

	for _, ci := range containers {
		if !isNginxContainer(ci) && !isProxyCandidate(ci) {
			continue
		}
		for _, d := range c.introspectNginxContainer(ctx, ci.ID) {
			add(d)
		}
	}

	c.cacheMutex.Lock()
	c.discoveredNginxDirs = found
	c.nginxDiscoveryAt = time.Now()
	c.cacheMutex.Unlock()
	if len(found) > 0 {
		c.logger.Debug("Discovered nginx config dirs from container introspection", "dirs", found)
	}
	return found
}

// introspectNginxContainer maps an nginx/NPM container's data/config bind mounts
// to host paths holding its generated configs. All results are HOST paths.
func (c *dockerMetricsCollector) introspectNginxContainer(ctx context.Context, id string) []string {
	info, err := c.client.ContainerInspect(ctx, id)
	if err != nil {
		return nil
	}
	var out []string
	for _, m := range info.Mounts {
		if m.Source == "" {
			continue
		}
		dest := strings.ToLower(filepath.Clean(m.Destination))
		switch {
		case dest == "/data": // NPM data volume → generated proxy_host configs
			out = append(out, filepath.Join(m.Source, "nginx", "proxy_host"))
		case dest == "/etc/nginx":
			out = append(out, m.Source, filepath.Join(m.Source, "conf.d"))
		case strings.Contains(dest, "proxy_host") || strings.HasSuffix(dest, "/conf.d"):
			out = append(out, m.Source)
		}
	}
	return out
}
