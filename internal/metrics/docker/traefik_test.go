package docker

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/docker/docker/api/types/container"
)

const sampleDynamicYAML = `http:
  routers:
    ammocrypt-frontend-vfi8us-router-5:
      rule: Host(` + "`ammocrypt.example.com`" + `)
      service: ammocrypt-frontend-vfi8us-service-5
      entryPoints:
        - web
  services:
    ammocrypt-frontend-vfi8us-service-5:
      loadBalancer:
        servers:
          - url: http://tasks.ammocrypt-frontend-vfi8us:3000
        passHostHeader: true
`

const sampleTLSYAML = `http:
  routers:
    web-router:
      rule: Host(` + "`secure.example.com`" + `)
      service: web-svc
      entryPoints:
        - websecure
  services:
    web-svc:
      loadBalancer:
        servers:
          - url: http://board-app-web:8080
`

func writeDynamicDir(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return dir
}

func TestParseTraefikDirs(t *testing.T) {
	dir := writeDynamicDir(t, map[string]string{
		"ammocrypt-frontend-vfi8us.yml": sampleDynamicYAML,
		"secure.yml":                    sampleTLSYAML,
		"acme.json":                     "{}", // ACME present → http routers upgrade to https for FQDNs
		"access.log":                    "noise that must be ignored",
	})

	routes := parseTraefikDirs([]string{dir})
	byHost := map[string]traefikRoute{}
	for _, r := range routes {
		byHost[r.Host] = r
	}

	a, ok := byHost["ammocrypt.example.com"]
	if !ok {
		t.Fatalf("missing route for ammocrypt.example.com; got %+v", routes)
	}
	if a.UpstreamHost != "ammocrypt-frontend-vfi8us" {
		t.Fatalf("upstream host = %q, want ammocrypt-frontend-vfi8us (tasks. stripped)", a.UpstreamHost)
	}
	if a.UpstreamPort != 3000 {
		t.Fatalf("upstream port = %d, want 3000", a.UpstreamPort)
	}
	// web entrypoint but ACME present + FQDN → https.
	if a.Scheme != "https" || a.URL() != "https://ammocrypt.example.com" {
		t.Fatalf("scheme/url = %q / %q, want https", a.Scheme, a.URL())
	}

	s := byHost["secure.example.com"]
	if s.Scheme != "https" || s.UpstreamPort != 8080 || s.UpstreamHost != "board-app-web" {
		t.Fatalf("secure route = %+v, want https board-app-web:8080", s)
	}
}

func TestParseTraefikDirs_NoACME_StaysHTTP(t *testing.T) {
	dir := writeDynamicDir(t, map[string]string{"a.yml": sampleDynamicYAML})
	routes := parseTraefikDirs([]string{dir})
	if len(routes) != 1 {
		t.Fatalf("got %d routes, want 1", len(routes))
	}
	// web entrypoint, no ACME, no tls → http.
	if routes[0].Scheme != "http" {
		t.Fatalf("scheme = %q, want http without ACME/tls", routes[0].Scheme)
	}
}

func TestRouteMatchesContainer(t *testing.T) {
	r := traefikRoute{UpstreamHost: "ammocrypt-frontend-vfi8us", UpstreamPort: 3000, Host: "x", Scheme: "http"}
	tests := []struct {
		name string
		c    DockerContainer
		want bool
	}{
		{"swarm task name prefix", DockerContainer{Name: "ammocrypt-frontend-vfi8us.1.g660abc", Project: "ammocrypt-frontend-vfi8us"}, true},
		{"by project", DockerContainer{Name: "whatever", Project: "ammocrypt-frontend-vfi8us"}, true},
		{"compose alias dash", DockerContainer{Name: "x", Project: "board", Service: "web"}, false},
		{"unrelated", DockerContainer{Name: "redis-x.1.aaa", Project: "redis-x"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := routeMatchesContainer(r, &tt.c); got != tt.want {
				t.Fatalf("routeMatchesContainer = %v, want %v", got, tt.want)
			}
		})
	}

	// compose network alias "<project>-<service>"
	rc := traefikRoute{UpstreamHost: "board-web", UpstreamPort: 80, Host: "h", Scheme: "http"}
	c := DockerContainer{Name: "board-web-1", Project: "board", Service: "web"}
	if !routeMatchesContainer(rc, &c) {
		t.Fatal("expected compose alias board-web to match project=board service=web")
	}
}

func TestEnrichWithTraefikRoutes_AttachesAndSynthesizes(t *testing.T) {
	routes := []traefikRoute{
		// matches a published port → URL attached to it
		{Host: "api.example.com", Scheme: "https", UpstreamHost: "api-svc", UpstreamPort: 8080},
		// matches a container with NO listed port → synthetic port added
		{Host: "front.example.com", Scheme: "https", UpstreamHost: "front-svc", UpstreamPort: 3000},
	}
	m := &DockerMetric{Stacks: []DockerStack{{Containers: []DockerContainer{
		{Name: "api-svc.1.aaa", Project: "api-svc", Ports: []DockerPort{{PrivatePort: 8080, Type: "tcp"}}},
		{Name: "front-svc.1.bbb", Project: "front-svc", Ports: []DockerPort{}},
	}}}}

	enrichWithTraefikRoutes(m, routes, nil)

	api := m.Stacks[0].Containers[0]
	if len(api.Ports) != 1 || api.Ports[0].PublicURL != "https://api.example.com" {
		t.Fatalf("api ports = %+v, want url on 8080", api.Ports)
	}
	front := m.Stacks[0].Containers[1]
	if len(front.Ports) != 1 || front.Ports[0].PrivatePort != 3000 || front.Ports[0].PublicURL != "https://front.example.com" {
		t.Fatalf("front ports = %+v, want synthetic 3000 with url", front.Ports)
	}
	if got := firstContainerPortURL(front); got != "https://front.example.com" {
		t.Fatalf("firstContainerPortURL = %q", got)
	}
}

func TestEnrichWithTraefikRoutes_SkipsAmbiguousCrossProject(t *testing.T) {
	// A bare upstream "web" matches a compose service "web" in project "myapp"
	// AND an unrelated standalone container literally named "web" → ambiguous
	// across projects, so neither should be labelled.
	routes := []traefikRoute{{Host: "site.example.com", Scheme: "https", UpstreamHost: "web", UpstreamPort: 80}}
	m := &DockerMetric{Stacks: []DockerStack{{Containers: []DockerContainer{
		{Name: "myapp-web-1", Project: "myapp", Service: "web", Ports: []DockerPort{{PrivatePort: 80, Type: "tcp"}}},
		{Name: "web", Project: "web", Ports: []DockerPort{{PrivatePort: 80, Type: "tcp"}}},
	}}}}

	enrichWithTraefikRoutes(m, routes, nil)

	for _, c := range m.Stacks[0].Containers {
		for _, p := range c.Ports {
			if p.PublicURL != "" {
				t.Fatalf("ambiguous cross-project route should not attach; %s got %q", c.Name, p.PublicURL)
			}
		}
	}
}

func TestEnrichWithTraefikRoutes_AttachesToAllReplicasInOneProject(t *testing.T) {
	// Two replicas of one swarm service share a project → both get the URL.
	routes := []traefikRoute{{Host: "svc.example.com", Scheme: "https", UpstreamHost: "websvc", UpstreamPort: 8080}}
	m := &DockerMetric{Stacks: []DockerStack{{Containers: []DockerContainer{
		{Name: "websvc.1.aaa", Project: "websvc", Ports: []DockerPort{{PrivatePort: 8080, Type: "tcp"}}},
		{Name: "websvc.2.bbb", Project: "websvc", Ports: []DockerPort{{PrivatePort: 8080, Type: "tcp"}}},
	}}}}

	enrichWithTraefikRoutes(m, routes, nil)

	for _, c := range m.Stacks[0].Containers {
		if c.Ports[0].PublicURL != "https://svc.example.com" {
			t.Fatalf("replica %s missing URL: %+v", c.Name, c.Ports)
		}
	}
}

func TestHostsFromRule_MultipleDomains(t *testing.T) {
	cases := []struct {
		rule string
		want []string
	}{
		{"Host(`a.example.com`)", []string{"a.example.com"}},
		{"Host(`a.example.com`) || Host(`b.example.com`)", []string{"a.example.com", "b.example.com"}},
		{"Host(`a.example.com`, `b.example.com`)", []string{"a.example.com", "b.example.com"}},
		{"Host(`a.example.com`) && PathPrefix(`/api`)", []string{"a.example.com"}},
		{"HostRegexp(`{sub:.+}.x.com`)", nil}, // wildcard-ish → skipped
		{"PathPrefix(`/only-path`)", nil},
	}
	for _, tc := range cases {
		got := hostsFromRule(tc.rule)
		if len(got) != len(tc.want) {
			t.Fatalf("hostsFromRule(%q) = %v, want %v", tc.rule, got, tc.want)
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Fatalf("hostsFromRule(%q) = %v, want %v", tc.rule, got, tc.want)
			}
		}
	}
}

// Two routers → ONE upstream service:port (the dokploy pattern: two domains for
// one app). Both URLs must survive on the container — the second must not be
// silently dropped because the port slot already carries the first.
func TestEnrichWithTraefikRoutes_MultipleDomainsOneService(t *testing.T) {
	routes := []traefikRoute{
		{Host: "ebcenter.sky-tehnol.uk", Scheme: "https", UpstreamHost: "ebcenter-app-yxkjot", UpstreamPort: 3000},
		{Host: "prosmety.by", Scheme: "https", UpstreamHost: "ebcenter-app-yxkjot", UpstreamPort: 3000},
	}
	m := &DockerMetric{Stacks: []DockerStack{{Containers: []DockerContainer{
		{Name: "ebcenter-app-yxkjot.1.sgmey", Project: "ebcenter-app-yxkjot", Ports: []DockerPort{}},
	}}}}

	enrichWithTraefikRoutes(m, routes, nil)

	c := m.Stacks[0].Containers[0]
	urls := map[string]bool{}
	for _, p := range c.Ports {
		if p.PublicURL != "" {
			urls[p.PublicURL] = true
		}
	}
	if !urls["https://ebcenter.sky-tehnol.uk"] || !urls["https://prosmety.by"] {
		t.Fatalf("expected BOTH domains attached, got ports %+v", c.Ports)
	}

	// Re-running enrichment (next collection tick) must not duplicate entries.
	enrichWithTraefikRoutes(m, routes, nil)
	if n := len(m.Stacks[0].Containers[0].Ports); n != 2 {
		t.Fatalf("idempotency: got %d port rows after second enrich, want 2", n)
	}
}

func TestParseTraefikDirs_MultiHostRouter(t *testing.T) {
	multi := `http:
  routers:
    app-router:
      rule: Host(` + "`one.example.com`" + `) || Host(` + "`two.example.com`" + `)
      service: app-svc
      entryPoints:
        - websecure
  services:
    app-svc:
      loadBalancer:
        servers:
          - url: http://tasks.app:3000
`
	dir := writeDynamicDir(t, map[string]string{"app.yml": multi})
	routes := parseTraefikDirs([]string{dir})
	if len(routes) != 2 {
		t.Fatalf("got %d routes, want 2 (one per domain): %+v", len(routes), routes)
	}
	for _, r := range routes {
		if r.UpstreamHost != "app" || r.UpstreamPort != 3000 || r.Scheme != "https" {
			t.Fatalf("route %+v: want upstream app:3000 https", r)
		}
	}
}

func TestContainerPathToHost(t *testing.T) {
	mounts := []container.MountPoint{
		{Type: "bind", Source: "/etc/dokploy/traefik", Destination: "/etc/traefik"},
		{Type: "bind", Source: "/etc/dokploy/traefik/dynamic", Destination: "/etc/traefik/dynamic"},
	}
	// Longest destination prefix wins.
	if got := containerPathToHost("/etc/traefik/dynamic", mounts); got != "/etc/dokploy/traefik/dynamic" {
		t.Fatalf("got %q", got)
	}
	if got := containerPathToHost("/etc/traefik/traefik.yml", mounts); got != "/etc/dokploy/traefik/traefik.yml" {
		t.Fatalf("got %q", got)
	}
	// Unmapped paths pass through.
	if got := containerPathToHost("/data/conf", mounts); got != "/data/conf" {
		t.Fatalf("got %q", got)
	}
}
