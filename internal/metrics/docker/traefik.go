package docker

import (
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	yaml "gopkg.in/yaml.v2"
)

// traefikRoute is one resolved Traefik file-provider route: a public host rule
// pointing at an upstream service:port. It is provider-generic — the same
// format Traefik's file provider uses (dokploy, plain Traefik, etc.).
type traefikRoute struct {
	Host         string // public hostname from the Host(`…`) rule
	Scheme       string // http | https
	UpstreamHost string // service/container the router forwards to (tasks. stripped)
	UpstreamPort int    // internal container port the router targets
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
	return scheme + "://" + r.Host
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

// parseTraefikDirs reads every *.yml/*.yaml document under the given dirs,
// merges all routers + services (so cross-file references resolve), and
// returns the resolved routes. Missing/unreadable dirs are skipped silently.
func parseTraefikDirs(dirs []string) []traefikRoute {
	routers := map[string]traefikRouterSpec{}
	services := map[string]traefikServiceSpec{}
	dirHasACME := false

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
			for k, v := range doc.HTTP.Routers {
				routers[k] = v
			}
			for k, v := range doc.HTTP.Services {
				services[k] = v
			}
		}
	}

	var out []traefikRoute
	for _, rt := range routers {
		host := firstHostRule(rt.Rule)
		if host == "" {
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
		out = append(out, traefikRoute{
			Host:         host,
			Scheme:       traefikScheme(rt.EntryPoints, rt.TLS != nil, dirHasACME, host),
			UpstreamHost: upstreamHost,
			UpstreamPort: upstreamPort,
		})
	}
	return out
}

// firstHostRule extracts the first Host(`…`) hostname from a Traefik router
// rule (reusing traefikHostRule defined in applications.go).
func firstHostRule(rule string) string {
	if m := traefikHostRule.FindStringSubmatch(rule); len(m) == 2 {
		return strings.TrimSpace(m[1])
	}
	return ""
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

// enrichWithTraefikRoutes attaches Traefik-derived public URLs to each
// container's ports. A route whose upstream port isn't an already-known port
// is appended as a synthetic (unpublished) port carrying the URL, so the UI
// can surface "reachable at <url>" even when the container publishes nothing.
func enrichWithTraefikRoutes(m *DockerMetric, routes []traefikRoute) {
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
			continue
		}
		for _, c := range matches {
			attachRouteURL(c, r)
		}
	}
}

// routeMatchesContainer reports whether a Traefik route's upstream host names
// this container, matching across compose/swarm naming conventions.
func routeMatchesContainer(r traefikRoute, c *DockerContainer) bool {
	h := r.UpstreamHost
	if h == "" {
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

// attachRouteURL records the route's URL on the matching private port, or
// appends a synthetic unpublished port when the target port isn't listed.
func attachRouteURL(c *DockerContainer, r traefikRoute) {
	u := r.URL()
	for i := range c.Ports {
		if r.UpstreamPort == 0 || c.Ports[i].PrivatePort == r.UpstreamPort {
			if c.Ports[i].PublicURL == "" {
				c.Ports[i].PublicURL = u
			}
			return
		}
	}
	c.Ports = append(c.Ports, DockerPort{PrivatePort: r.UpstreamPort, Type: "tcp", PublicURL: u})
}

// firstContainerPortURL returns the first Traefik URL attached to any of the
// container's ports, used as the app-level public link when no label provided one.
func firstContainerPortURL(c DockerContainer) string {
	for _, p := range c.Ports {
		if p.PublicURL != "" {
			return p.PublicURL
		}
	}
	return ""
}
