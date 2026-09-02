package gateway

import (
	"context"
	"strings"
	"testing"

	"gopkg.in/yaml.v2"
)

func TestRender_RouteLimits(t *testing.T) {
	cfg := Config{Enabled: true, Mode: ModeManaged}
	routes := []Route{{
		RouteID: "lim1", Domain: "admin.example.com", TargetScheme: "https", TargetInsecureSkipVerify: true,
		TargetHost: "10.0.0.5", TargetPort: 8443, TLS: true, Enabled: true,
		IPAllowList: "10.0.0.0/8", BasicAuthUsers: "admin:$2a$10$hash",
		MaxConnsPerIP: 100, RateLimitRPS: 20, ReadOnly: true, UpstreamTimeoutSeconds: 60, MaxBodyBytes: 32 << 20,
	}}
	out, err := renderRoutes(t, cfg, routes, nil)
	if err != nil {
		t.Fatal(err)
	}
	var doc traefikDynamic
	if err := yaml.Unmarshal(out, &doc); err != nil {
		t.Fatalf("yaml: %v\n%s", err, out)
	}
	r := doc.HTTP.Routers["ns-lim1"]
	// Traefik v3: Method() takes one argument — must be an OR chain.
	if !strings.Contains(r.Rule, "(Method(`GET`) || Method(`HEAD`) || Method(`OPTIONS`))") || strings.Contains(r.Rule, "Method(`GET`,") {
		t.Errorf("read-only rule: %s", r.Rule)
	}
	want := []string{"ns-lim1-ipallow", "ns-lim1-ratelimit", "ns-lim1-inflight", "ns-lim1-auth", "ns-lim1-body"}
	if strings.Join(r.Middlewares, ",") != strings.Join(want, ",") {
		t.Errorf("middleware order = %v, want %v", r.Middlewares, want)
	}
	if mw := doc.HTTP.Middlewares["ns-lim1-inflight"]; mw.InFlightReq == nil || mw.InFlightReq.Amount != 100 {
		t.Errorf("inflight: %+v", mw)
	}
	if mw := doc.HTTP.Middlewares["ns-lim1-ratelimit"]; mw.RateLimit == nil || mw.RateLimit.Average != 20 || mw.RateLimit.Burst != 40 {
		t.Errorf("ratelimit: %+v", mw)
	}
	if mw := doc.HTTP.Middlewares["ns-lim1-body"]; mw.Buffering == nil || mw.Buffering.MaxRequestBodyBytes != 32<<20 {
		t.Errorf("buffering: %+v", mw)
	}
	tr := doc.HTTP.ServersTransports["ns-lim1-transport"]
	if !tr.InsecureSkipVerify || tr.ForwardingTimeouts == nil || tr.ForwardingTimeouts.ResponseHeaderTimeout != "60s" {
		t.Errorf("transport: %+v", tr)
	}
	if doc.HTTP.Services["ns-lim1"].LoadBalancer.ServersTransport != "ns-lim1-transport" {
		t.Errorf("service must reference the transport")
	}
}

// An upstream timeout alone (plain http target) still needs a transport; a
// route with no limits renders exactly as before (no extra middlewares).
func TestRender_UpstreamTimeoutWithoutSkipVerify_AndNoLimits(t *testing.T) {
	cfg := Config{Enabled: true, Mode: ModeManaged}
	routes := []Route{
		{RouteID: "t1", Domain: "a.example.com", TargetScheme: "http", TargetHost: "h", TargetPort: 80, Enabled: true, UpstreamTimeoutSeconds: 15},
		{RouteID: "t2", Domain: "b.example.com", TargetScheme: "http", TargetHost: "h", TargetPort: 80, Enabled: true},
	}
	out, err := renderRoutes(t, cfg, routes, nil)
	if err != nil {
		t.Fatal(err)
	}
	var doc traefikDynamic
	_ = yaml.Unmarshal(out, &doc)
	tr, ok := doc.HTTP.ServersTransports["ns-t1-transport"]
	if !ok || tr.InsecureSkipVerify || tr.ForwardingTimeouts.ResponseHeaderTimeout != "15s" {
		t.Errorf("t1 transport: %+v ok=%v", tr, ok)
	}
	if _, ok := doc.HTTP.ServersTransports["ns-t2-transport"]; ok {
		t.Error("t2 must not get a transport")
	}
	if r := doc.HTTP.Routers["ns-t2"]; len(r.Middlewares) != 0 || strings.Contains(r.Rule, "Method") {
		t.Errorf("t2 router must be plain: %+v", r)
	}
}

func TestConfigEffectiveRequestReadTimeout(t *testing.T) {
	if got := (Config{}).EffectiveRequestReadTimeoutSeconds(); got != 86400 {
		t.Errorf("unset → %d, want 86400", got)
	}
	if got := (Config{RequestReadTimeoutSeconds: -1}).EffectiveRequestReadTimeoutSeconds(); got != 0 {
		t.Errorf("unlimited → %d, want 0", got)
	}
	if got := (Config{RequestReadTimeoutSeconds: 600}).EffectiveRequestReadTimeoutSeconds(); got != 600 {
		t.Errorf("600 → %d", got)
	}
}

// The upsert column list must carry every replicated field — a column missing
// there silently reverts on every replica (the class of bug behind the blocks
// table's c_id_r incident).
func TestRouteRepositoryUpsertRoundTripLimits(t *testing.T) {
	db := openTestDB(t)
	if err := AutoMigrate(db); err != nil {
		t.Fatal(err)
	}
	repo := NewRepository(db)
	ctx := context.Background()
	r := Route{RouteID: "r1", Domain: "a.example.com", TargetScheme: "http", TargetHost: "h", TargetPort: 80, Enabled: true}
	if err := repo.Upsert(ctx, &r); err != nil {
		t.Fatal(err)
	}
	r.MaxConnsPerIP, r.RateLimitRPS, r.ReadOnly, r.UpstreamTimeoutSeconds, r.MaxBodyBytes = 100, 5, true, 60, 1<<20
	if err := repo.Upsert(ctx, &r); err != nil {
		t.Fatal(err)
	}
	got, err := repo.GetByRouteID(ctx, "r1")
	if err != nil {
		t.Fatal(err)
	}
	if got.MaxConnsPerIP != 100 || got.RateLimitRPS != 5 || !got.ReadOnly || got.UpstreamTimeoutSeconds != 60 || got.MaxBodyBytes != 1<<20 {
		t.Errorf("limits did not survive upsert: %+v", got)
	}
	for _, col := range []string{"max_conns_per_ip", "rate_limit_rps", "read_only", "upstream_timeout_seconds", "max_body_bytes"} {
		if !db.Migrator().HasColumn(&Route{}, col) {
			t.Errorf("missing pinned column %s", col)
		}
	}
}
