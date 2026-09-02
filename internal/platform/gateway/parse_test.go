package gateway

import (
	"strings"
	"testing"
)

func TestParseDynamic_RoundTripsRenderedRoutes(t *testing.T) {
	cfg := Config{Enabled: true, Mode: ModeManaged, ACMEEnabled: true}
	in := []Route{
		{RouteID: "aaaaaaaaaaa1", Domain: "app.example.com", Aliases: "www.app.example.com", TargetScheme: SchemeHTTPS, TargetHost: "10.0.0.5", TargetPort: 8443,
			TargetInsecureSkipVerify: true, TargetServerName: "app.internal", TLS: true, Enabled: true, PathPrefix: "/grafana,/g2", StripPrefix: true, AddPrefix: "/ui",
			HostHeaderMode: HostHeaderCustom, HostHeaderValue: "app.internal", ExtraTargets: "10.0.0.6:8443", HealthCheckPath: "/healthz", HealthCheckIntervalSeconds: 30,
			Sticky: true, RetryAttempts: 2, RequestHeaders: "X-A: 1", ResponseHeaders: "X-B: 2", ForwardAuthURL: "http://auth:9091/verify", ForwardAuthResponseHeaders: "Remote-User",
			ForwardAuthTrustForwardHeader: true, SecurityHeaders: true, HSTS: true, HSTSIncludeSubdomains: true, Compress: true, IPAllowList: "10.0.0.0/8",
			MaxBodyBytes: 1 << 20, MaxConnsPerIP: 10, RateLimitRPS: 5, UpstreamTimeoutSeconds: 60, ReadOnly: true, BasicAuthUsers: "u:$2a$10$hash"},
		{RouteID: "aaaaaaaaaaa2", Domain: "nb.example.com", TargetScheme: SchemeH2C, TargetHost: "netbird-server", TargetPort: 80, TLS: true, Enabled: true, PathPrefix: "/signalexchange.SignalExchange,/management.ManagementService"},
		{RouteID: "aaaaaaaaaaa3", Domain: "up.example.com", TargetHost: "10.0.0.8", TargetPort: 80, Enabled: true, HostHeaderMode: HostHeaderUpstream},
		{RouteID: "aaaaaaaaaaa4", Mode: RouteModeRedirect, Domain: "old.example.com", Aliases: "www.old.example.com", TLS: true, Enabled: true, RedirectURL: "https://new.example.com", RedirectPermanent: true, RedirectPreservePath: true, TargetHost: "x", TargetPort: 80},
		{RouteID: "aaaaaaaaaaa5", Mode: RouteModeStream, Protocol: ProtoTCP, ListenPort: 25565, TargetHost: "10.0.0.9", TargetPort: 25565, ExtraTargets: "10.0.0.10:25565", Enabled: true},
		{RouteID: "aaaaaaaaaaa6", Mode: RouteModeStream, Protocol: ProtoUDP, ListenPort: 64738, TargetHost: "10.0.0.9", TargetPort: 64738, Enabled: true},
		{RouteID: "aaaaaaaaaaa7", Mode: RouteModePassthrough, Domain: "*.apps.example.com", Aliases: "apps.example.com", TargetHost: "10.0.0.11", TargetPort: 8080, TargetHTTPSPort: 8443, Enabled: true},
	}
	files, err := RenderFiles(cfg, in, nil)
	if err != nil {
		t.Fatal(err)
	}
	out, warnings, err := ParseDynamic(files.Routes)
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 0 {
		t.Errorf("unexpected warnings: %v", warnings)
	}
	byID := map[string]Route{}
	for _, r := range out {
		byID[r.RouteID] = r
	}
	if len(byID) != len(in) {
		t.Fatalf("got %d routes, want %d: %+v", len(byID), len(in), out)
	}
	for _, want := range in {
		got, ok := byID[want.RouteID]
		if !ok {
			t.Errorf("route %s missing", want.RouteID)
			continue
		}
		// Fields the file carries verbatim must survive; Name/Enabled/timestamps are not in the file.
		want.Name, want.Enabled, want.TargetScheme = "", true, firstNonEmpty(want.TargetScheme, SchemeHTTP)
		want.Mode = firstNonEmpty(want.Mode, RouteModeHTTP)
		got.Name = ""
		if want.IsRedirect() {
			want.TargetHost, want.TargetPort, want.TargetScheme = "redirect", 80, SchemeHTTP
		}
		if want.IsStream() {
			want.TargetScheme = want.Protocol
		}
		if got != want {
			t.Errorf("route %s differs:\n got  %+v\n want %+v", want.RouteID, got, want)
		}
	}
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func TestParseDynamic_ForeignFileAndWarnings(t *testing.T) {
	raw := `
http:
  routers:
    grafana:
      rule: "Host(` + "`grafana.example.com`, `g.example.com`" + `) && PathPrefix(` + "`/grafana`" + `)"
      entryPoints: [websecure]
      service: grafana-svc@file
      middlewares: [auth@file, weird]
      tls:
        certResolver: le
    grafana-http:
      rule: "Host(` + "`grafana.example.com`, `g.example.com`" + `) && PathPrefix(` + "`/grafana`" + `)"
      entryPoints: [web]
      service: grafana-svc
      middlewares: [to-https]
    nohost:
      rule: "PathPrefix(` + "`/x`" + `)"
      service: grafana-svc
  services:
    grafana-svc:
      loadBalancer:
        servers:
          - url: http://grafana:3000
          - url: http://grafana2:3000
        passHostHeader: false
  middlewares:
    auth:
      basicAuth:
        users: ["admin:$apr1$abc"]
    to-https:
      redirectScheme:
        scheme: https
        permanent: true
    weird:
      replacePath:
        path: /foo
`
	routes, warnings, err := ParseDynamic([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if len(routes) != 1 {
		t.Fatalf("routes: %+v", routes)
	}
	r := routes[0]
	if r.RouteID != "" || r.Name != "grafana" || r.Domain != "grafana.example.com" || r.Aliases != "g.example.com" || r.PathPrefix != "/grafana" || !r.TLS ||
		r.TargetHost != "grafana" || r.TargetPort != 3000 || r.ExtraTargets != "grafana2:3000" || r.HostHeaderMode != HostHeaderUpstream || r.BasicAuthUsers != "admin:$apr1$abc" {
		t.Errorf("foreign route: %+v", r)
	}
	joined := strings.Join(warnings, "\n")
	for _, want := range []string{"nohost", "weird"} {
		if !strings.Contains(joined, want) {
			t.Errorf("warnings missing %q: %v", want, warnings)
		}
	}
	if _, _, err := ParseDynamic([]byte("routes: []\n")); err == nil {
		t.Error("a non-Traefik document must be rejected")
	}
}
