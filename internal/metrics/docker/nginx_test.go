package docker

import (
	"archive/tar"
	"bytes"
	"testing"
)

func TestParseNginxTar(t *testing.T) {
	// Mimic `docker cp` of NPM's proxy_host dir: a tar with a dir entry + .conf
	// files (one NPM variable-upstream, one with a non-.conf file to be skipped).
	files := []struct {
		name, body string
		dir        bool
	}{
		{name: "proxy_host/", dir: true},
		{name: "proxy_host/1.conf", body: `
server {
  set $forward_scheme http;
  set $server "wg-easy";
  set $port 51821;
  listen 443 ssl;
  server_name wg.example.com;
  location / { proxy_pass $forward_scheme://$server:$port; }
}`},
		{name: "proxy_host/2.conf", body: `
server {
  set $forward_scheme http;
  set $server "172.17.0.1";
  set $port 9090;
  listen 443 ssl;
  server_name dash.example.com;
  location / { proxy_pass $forward_scheme://$server:$port; }
}`},
		{name: "proxy_host/.keep", body: "ignore me"},
	}
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, f := range files {
		hdr := &tar.Header{Name: f.name, Mode: 0o644, Size: int64(len(f.body))}
		if f.dir {
			hdr.Typeflag = tar.TypeDir
			hdr.Size = 0
		} else {
			hdr.Typeflag = tar.TypeReg
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if !f.dir {
			_, _ = tw.Write([]byte(f.body))
		}
	}
	tw.Close()

	routes := parseNginxTar(&buf)
	if len(routes) != 2 {
		t.Fatalf("got %d routes, want 2: %+v", len(routes), routes)
	}
	byHost := map[string]traefikRoute{}
	for _, r := range routes {
		byHost[r.Host] = r
	}
	if r, ok := byHost["wg.example.com"]; !ok || r.UpstreamHost != "wg-easy" || r.UpstreamIsIP || r.Scheme != "https" {
		t.Errorf("wg route = %+v", r)
	}
	if r, ok := byHost["dash.example.com"]; !ok || r.UpstreamHost != "172.17.0.1" || !r.UpstreamIsIP || r.UpstreamPort != 9090 {
		t.Errorf("dash route = %+v", r)
	}
}

func TestRoutesFromNginxConf_NPMVariableUpstream(t *testing.T) {
	// Nginx Proxy Manager generated proxy_host config: forward target via
	// `set $server/$port`, both :80 and :443 in one server block.
	conf := `
# ------------------------------------------------------------
# app.example.com
# ------------------------------------------------------------
server {
  set $forward_scheme http;
  set $server         "192.168.1.50";
  set $port           8096;

  listen 80;
  listen [::]:80;
  listen 443 ssl;
  server_name app.example.com;

  location / {
    proxy_set_header Host $host;
    proxy_pass $forward_scheme://$server:$port;
  }
}
`
	routes := routesFromNginxConf(conf)
	if len(routes) != 1 {
		t.Fatalf("got %d routes, want 1: %+v", len(routes), routes)
	}
	r := routes[0]
	if r.Host != "app.example.com" || r.Scheme != "https" {
		t.Errorf("host/scheme = %q/%q, want app.example.com/https", r.Host, r.Scheme)
	}
	if r.UpstreamHost != "192.168.1.50" || r.UpstreamPort != 8096 || !r.UpstreamIsIP {
		t.Errorf("upstream = %q:%d isIP=%v, want 192.168.1.50:8096 isIP=true", r.UpstreamHost, r.UpstreamPort, r.UpstreamIsIP)
	}
}

func TestRoutesFromNginxConf_GenericContainerUpstream(t *testing.T) {
	conf := `
server {
  listen 80;
  server_name blog.example.com;
  location / {
    proxy_pass http://blog-backend:2368;
  }
}
`
	routes := routesFromNginxConf(conf)
	if len(routes) != 1 {
		t.Fatalf("got %d routes, want 1", len(routes))
	}
	r := routes[0]
	if r.Host != "blog.example.com" || r.Scheme != "http" {
		t.Errorf("host/scheme = %q/%q", r.Host, r.Scheme)
	}
	if r.UpstreamHost != "blog-backend" || r.UpstreamPort != 2368 || r.UpstreamIsIP {
		t.Errorf("upstream = %q:%d isIP=%v, want blog-backend:2368 isIP=false", r.UpstreamHost, r.UpstreamPort, r.UpstreamIsIP)
	}
}

func TestRoutesFromNginxConf_HTTPSProxyBlockWinsOverRedirect(t *testing.T) {
	// A :80 redirect block + a :443 proxy block for the same host → one route,
	// https with the resolved upstream.
	conf := `
server {
  listen 80;
  server_name shop.example.com;
  return 301 https://$host$request_uri;
}
server {
  listen 443 ssl;
  server_name shop.example.com;
  location / { proxy_pass http://shop:3000; }
}
`
	routes := routesFromNginxConf(conf)
	if len(routes) != 1 {
		t.Fatalf("got %d routes, want 1: %+v", len(routes), routes)
	}
	r := routes[0]
	if r.Scheme != "https" || r.UpstreamHost != "shop" || r.UpstreamPort != 3000 {
		t.Errorf("route = %+v, want https shop:3000", r)
	}
}

func TestRoutesFromNginxConf_SkipsCatchAllAndWildcard(t *testing.T) {
	conf := `
server { listen 80 default_server; server_name _; location / { proxy_pass http://x:1; } }
server { listen 80; server_name *.example.com; location / { proxy_pass http://y:2; } }
`
	if routes := routesFromNginxConf(conf); len(routes) != 0 {
		t.Fatalf("got %d routes, want 0 (catch-all + wildcard skipped): %+v", len(routes), routes)
	}
}

func TestEnrichWithProxyRoutes_NginxIPUpstreamMatchesPublishedPort(t *testing.T) {
	// NPM forwards to an IP:port → match the container that PUBLISHES that host
	// port, not by name or private port.
	routes := []traefikRoute{
		{Host: "app.example.com", Scheme: "https", UpstreamHost: "192.168.1.50", UpstreamPort: 8096, UpstreamIsIP: true},
	}
	m := &DockerMetric{Stacks: []DockerStack{{Containers: []DockerContainer{
		{Name: "jellyfin", Project: "media", Ports: []DockerPort{{PrivatePort: 8096, PublicPort: 8096, Type: "tcp"}}},
		// Decoy: same private port, different published port → must NOT match.
		{Name: "other", Project: "other", Ports: []DockerPort{{PrivatePort: 8096, PublicPort: 9999, Type: "tcp"}}},
	}}}}

	enrichWithProxyRoutes(m, routes, nil)

	if got := m.Stacks[0].Containers[0].Ports[0].PublicURL; got != "https://app.example.com" {
		t.Fatalf("jellyfin port URL = %q, want https://app.example.com", got)
	}
	if got := m.Stacks[0].Containers[1].Ports[0].PublicURL; got != "" {
		t.Fatalf("decoy container must not be labelled, got %q", got)
	}
}
