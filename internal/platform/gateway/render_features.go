package gateway

import (
	"fmt"
	"strings"
)

// Traefik shapes for the route features beyond plain proxying.

// UDP section — stream routes with Protocol udp.
type traefikUDP struct {
	Routers  map[string]traefikUDPRouter  `yaml:"routers,omitempty"`
	Services map[string]traefikUDPService `yaml:"services,omitempty"`
}

type traefikUDPRouter struct {
	EntryPoints []string `yaml:"entryPoints"`
	Service     string   `yaml:"service"`
}

type traefikUDPService struct {
	LoadBalancer traefikUDPLoadBalancer `yaml:"loadBalancer"`
}

type traefikUDPLoadBalancer struct {
	Servers []traefikUDPServer `yaml:"servers"`
}

type traefikUDPServer struct {
	Address string `yaml:"address"`
}

// headers middleware: custom request/response headers + the browser
// hardening set + HSTS. Only the fields that are set are emitted.
type traefikHeaders struct {
	CustomRequestHeaders    map[string]string `yaml:"customRequestHeaders,omitempty"`
	CustomResponseHeaders   map[string]string `yaml:"customResponseHeaders,omitempty"`
	CustomFrameOptionsValue string            `yaml:"customFrameOptionsValue,omitempty"`
	ContentTypeNosniff      bool              `yaml:"contentTypeNosniff,omitempty"`
	BrowserXSSFilter        bool              `yaml:"browserXssFilter,omitempty"`
	ReferrerPolicy          string            `yaml:"referrerPolicy,omitempty"`
	STSSeconds              int               `yaml:"stsSeconds,omitempty"`
	STSIncludeSubdomains    bool              `yaml:"stsIncludeSubdomains,omitempty"`
}

// forwardAuth: every request is first sent to Address; a non-2xx answer is
// returned to the client as-is (the SSO login page), a 2xx lets it through
// with AuthResponseHeaders copied onto the upstream request.
type traefikForwardAuth struct {
	Address             string   `yaml:"address"`
	TrustForwardHeader  bool     `yaml:"trustForwardHeader,omitempty"`
	AuthResponseHeaders []string `yaml:"authResponseHeaders,omitempty"`
	// MaxResponseBodySize caps the auth service's answer Traefik buffers (a
	// login page at most); unset, Traefik 3.7 warns about unbounded memory.
	MaxResponseBodySize int64 `yaml:"maxResponseBodySize,omitempty"`
}

// forwardAuthMaxResponseBytes: 1 MiB — plenty for any login/denied page.
const forwardAuthMaxResponseBytes = 1 << 20

type traefikCompress struct{}

type traefikStripPrefix struct {
	Prefixes []string `yaml:"prefixes"`
}

type traefikAddPrefix struct {
	Prefix string `yaml:"prefix"`
}

type traefikRetry struct {
	Attempts int `yaml:"attempts"`
}

// redirectRegex rewrites the whole request URL (scheme://host/path) so a
// redirect route can point at any URL, optionally keeping the path.
type traefikRedirectRegex struct {
	Regex       string `yaml:"regex"`
	Replacement string `yaml:"replacement"`
	Permanent   bool   `yaml:"permanent"`
}

type traefikHealthCheck struct {
	Path     string `yaml:"path"`
	Interval string `yaml:"interval"`
	Timeout  string `yaml:"timeout"`
}

type traefikSticky struct {
	Cookie traefikStickyCookie `yaml:"cookie"`
}

type traefikStickyCookie struct {
	Name     string `yaml:"name"`
	Secure   bool   `yaml:"secure,omitempty"`
	HTTPOnly bool   `yaml:"httpOnly"`
}

const secUDPRouters, secUDPServices = "udp.routers", "udp.services"

// noopService is Traefik's built-in "no upstream" service, used by redirect
// routers (the middleware answers before anything is forwarded).
const noopService = "noop@internal"

// hostsRule ORs the rule for every hostname of the route (domain + aliases).
func hostsRule(r Route) string {
	hs := r.Hostnames()
	if len(hs) == 1 {
		return hostRule(hs[0])
	}
	parts := make([]string, 0, len(hs))
	for _, h := range hs {
		parts = append(parts, hostRule(h))
	}
	return "(" + strings.Join(parts, " || ") + ")"
}

// hostSNIsRule is the TCP-router counterpart of hostsRule.
func hostSNIsRule(r Route) string {
	hs := r.Hostnames()
	if len(hs) == 1 {
		return hostSNIRule(hs[0])
	}
	parts := make([]string, 0, len(hs))
	for _, h := range hs {
		parts = append(parts, hostSNIRule(h))
	}
	return "(" + strings.Join(parts, " || ") + ")"
}

// ParseHeaderLines turns "Name: value" lines into a map (later lines win).
// Malformed lines (no colon, empty name) are skipped — validation rejects
// them at write time, so this is only defensive.
func ParseHeaderLines(s string) map[string]string {
	out := map[string]string{}
	for _, line := range SplitLines(s) {
		i := strings.IndexByte(line, ':')
		if i <= 0 {
			continue
		}
		name := strings.TrimSpace(line[:i])
		if name == "" {
			continue
		}
		out[name] = strings.TrimSpace(line[i+1:])
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// headersMiddleware assembles the route's headers middleware, or nil when the
// route needs none.
func headersMiddleware(r Route) (*traefikHeaders, []string) {
	h := &traefikHeaders{}
	var what []string
	if req := ParseHeaderLines(r.RequestHeaders); len(req) > 0 {
		h.CustomRequestHeaders = req
		what = append(what, fmt.Sprintf("%d request header(s)", len(req)))
	}
	if r.HostHeaderMode == HostHeaderCustom && strings.TrimSpace(r.HostHeaderValue) != "" {
		if h.CustomRequestHeaders == nil {
			h.CustomRequestHeaders = map[string]string{}
		}
		h.CustomRequestHeaders["Host"] = strings.TrimSpace(r.HostHeaderValue)
		what = append(what, "Host: "+strings.TrimSpace(r.HostHeaderValue))
	}
	if resp := ParseHeaderLines(r.ResponseHeaders); len(resp) > 0 {
		h.CustomResponseHeaders = resp
		what = append(what, fmt.Sprintf("%d response header(s)", len(resp)))
	}
	if r.SecurityHeaders {
		h.CustomFrameOptionsValue = "SAMEORIGIN"
		h.ContentTypeNosniff = true
		h.BrowserXSSFilter = true
		h.ReferrerPolicy = "strict-origin-when-cross-origin"
		what = append(what, "security headers")
	}
	if r.HSTS && r.TLS {
		h.STSSeconds = HSTSMaxAgeSeconds
		h.STSIncludeSubdomains = r.HSTSIncludeSubdomains
		what = append(what, "HSTS")
	}
	if len(what) == 0 {
		return nil, nil
	}
	return h, what
}

// renderRedirect emits a redirect route: routers on web (and websecure when
// TLS is on, so https://old.example.com gets a certificate and redirects too)
// whose only middleware rewrites the URL; no upstream.
func renderRedirect(doc *traefikDynamic, rc *renderCtx, base, label string, r Route, cfg Config) {
	target := strings.TrimSpace(r.RedirectURL)
	mw := base + "-redirect"
	re := traefikRedirectRegex{Permanent: r.RedirectPermanent}
	if r.RedirectPreservePath {
		re.Regex = `^https?://[^/]+(/.*)?$`
		re.Replacement = strings.TrimRight(target, "/") + "${1}"
	} else {
		re.Regex = `^.*$`
		re.Replacement = target
	}
	doc.HTTP.Middlewares[mw] = traefikMiddleware{RedirectRegex: &re}
	code := "302"
	if r.RedirectPermanent {
		code = "301"
	}
	what := code + " → " + target
	if r.RedirectPreservePath {
		what += " (path kept)"
	}
	rc.note(secHTTPMiddlewares, mw, label, what)
	rule := hostsRule(r)
	doc.HTTP.Routers[base] = traefikRouter{Rule: rule, Service: noopService, EntryPoints: []string{entryPointWeb}, Middlewares: []string{mw}}
	rc.note(secHTTPRouters, base, label, "redirect on :80")
	if r.TLS {
		tls := &traefikTLS{}
		if cfg.ACMEEnabled {
			tls.CertResolver = CertResolverName
		}
		doc.HTTP.Routers[base+"-tls"] = traefikRouter{Rule: rule, Service: noopService, EntryPoints: []string{entryPointWebSecure}, Middlewares: []string{mw}, TLS: tls}
		rc.note(secHTTPRouters, base+"-tls", label, "redirect on :443 (with certificate)")
	}
}

// renderStream emits a raw TCP or UDP forward on the route's own entrypoint.
func renderStream(doc *traefikDynamic, rc *renderCtx, base, label string, r Route) {
	proto := r.Protocol
	if proto == "" {
		proto = ProtoTCP
	}
	ep := StreamEntryPoint(proto, r.ListenPort)
	addrs := []string{fmt.Sprintf("%s:%d", r.TargetHost, r.TargetPort)}
	addrs = append(addrs, r.ExtraServers()...)
	what := fmt.Sprintf("%s :%d → %s", proto, r.ListenPort, strings.Join(addrs, ", "))
	if proto == ProtoUDP {
		if doc.UDP == nil {
			doc.UDP = &traefikUDP{Routers: map[string]traefikUDPRouter{}, Services: map[string]traefikUDPService{}}
		}
		servers := make([]traefikUDPServer, 0, len(addrs))
		for _, a := range addrs {
			servers = append(servers, traefikUDPServer{Address: a})
		}
		doc.UDP.Services[base] = traefikUDPService{LoadBalancer: traefikUDPLoadBalancer{Servers: servers}}
		doc.UDP.Routers[base] = traefikUDPRouter{EntryPoints: []string{ep}, Service: base}
		rc.note(secUDPRouters, base, label, what)
		rc.note(secUDPServices, base, label, "udp upstream(s)")
		return
	}
	if doc.TCP == nil {
		doc.TCP = &traefikTCP{Routers: map[string]traefikTCPRouter{}, Services: map[string]traefikTCPService{}}
	}
	servers := make([]traefikTCPServer, 0, len(addrs))
	for _, a := range addrs {
		servers = append(servers, traefikTCPServer{Address: a})
	}
	doc.TCP.Services[base] = traefikTCPService{LoadBalancer: traefikTCPLoadBalancer{Servers: servers}}
	doc.TCP.Routers[base] = traefikTCPRouter{Rule: "HostSNI(`*`)", Service: base, EntryPoints: []string{ep}}
	rc.note(secTCPRouters, base, label, what)
	rc.note(secTCPServices, base, label, "tcp upstream(s)")
}
