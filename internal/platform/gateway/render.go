package gateway

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v2"
)

// Traefik v3 file-provider dynamic configuration shapes (only the subset we
// emit). Field names follow Traefik's YAML exactly.
type traefikDynamic struct {
	HTTP traefikHTTP `yaml:"http"`
	TCP  *traefikTCP `yaml:"tcp,omitempty"`
}

// TCP section — used for TLS passthrough routes (SNI-routed raw TLS).
type traefikTCP struct {
	Routers  map[string]traefikTCPRouter  `yaml:"routers,omitempty"`
	Services map[string]traefikTCPService `yaml:"services,omitempty"`
}

type traefikTCPRouter struct {
	Rule        string         `yaml:"rule"`
	Service     string         `yaml:"service"`
	EntryPoints []string       `yaml:"entryPoints"`
	Priority    int            `yaml:"priority,omitempty"`
	TLS         *traefikTCPTLS `yaml:"tls,omitempty"`
}

type traefikTCPTLS struct {
	Passthrough bool `yaml:"passthrough"`
}

type traefikTCPService struct {
	LoadBalancer traefikTCPLoadBalancer `yaml:"loadBalancer"`
}

type traefikTCPLoadBalancer struct {
	Servers []traefikTCPServer `yaml:"servers"`
}

type traefikTCPServer struct {
	Address string `yaml:"address"`
}

type traefikHTTP struct {
	Routers     map[string]traefikRouter     `yaml:"routers,omitempty"`
	Services    map[string]traefikService    `yaml:"services,omitempty"`
	Middlewares map[string]traefikMiddleware `yaml:"middlewares,omitempty"`

	// ServersTransports carries per-route upstream TLS settings (skip-verify
	// for self-signed https targets).
	ServersTransports map[string]traefikServersTransport `yaml:"serversTransports,omitempty"`
}

type traefikRouter struct {
	Rule        string      `yaml:"rule"`
	Service     string      `yaml:"service"`
	EntryPoints []string    `yaml:"entryPoints"`
	Middlewares []string    `yaml:"middlewares,omitempty"`
	Priority    int         `yaml:"priority,omitempty"`
	TLS         *traefikTLS `yaml:"tls,omitempty"`
}

type traefikTLS struct {
	CertResolver string `yaml:"certResolver,omitempty"`
}

type traefikService struct {
	LoadBalancer traefikLoadBalancer `yaml:"loadBalancer"`
}

type traefikLoadBalancer struct {
	Servers          []traefikServer `yaml:"servers"`
	PassHostHeader   *bool           `yaml:"passHostHeader,omitempty"`
	ServersTransport string          `yaml:"serversTransport,omitempty"`
}

type traefikServer struct {
	URL string `yaml:"url"`
}

type traefikServersTransport struct {
	InsecureSkipVerify bool `yaml:"insecureSkipVerify"`
}

type traefikMiddleware struct {
	BasicAuth      *traefikBasicAuth      `yaml:"basicAuth,omitempty"`
	IPAllowList    *traefikIPAllowList    `yaml:"ipAllowList,omitempty"`
	RedirectScheme *traefikRedirectScheme `yaml:"redirectScheme,omitempty"`
}

type traefikBasicAuth struct {
	Users []string `yaml:"users"`
}

type traefikIPAllowList struct {
	SourceRange []string `yaml:"sourceRange"`
}

type traefikRedirectScheme struct {
	Scheme    string `yaml:"scheme"`
	Permanent bool   `yaml:"permanent"`
}

const (
	entryPointWeb       = "web"
	entryPointWebSecure = "websecure"
	// namePrefix namespaces every object node-stats owns so it can never
	// collide with the operator's routers/services in an adopted Traefik.
	namePrefix = "ns-"
)

// Render produces the Traefik dynamic YAML for the enabled routes. Output is
// deterministic (sorted maps via yaml.v2) so the materializer can diff bytes
// and only rewrite the file on real change.
//
// Per route:
//   - one service (loadBalancer → target URL)
//   - a router on `websecure` (tls) or `web` (plain)
//   - for TLS routes an extra `web` router redirecting to https
//   - optional basicAuth / ipAllowList middlewares
func Render(cfg Config, routes []Route, blocks []Block) ([]byte, error) {
	doc := traefikDynamic{HTTP: traefikHTTP{
		Routers:           map[string]traefikRouter{},
		Services:          map[string]traefikService{},
		Middlewares:       map[string]traefikMiddleware{},
		ServersTransports: map[string]traefikServersTransport{},
	}}

	sorted := append([]Route(nil), routes...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].RouteID < sorted[j].RouteID })

	for _, r := range sorted {
		if !r.Enabled || r.RouteID == "" || r.Domain == "" || r.TargetHost == "" || r.TargetPort <= 0 {
			continue
		}
		base := namePrefix + r.RouteID
		if r.IsPassthrough() {
			renderPassthrough(&doc, base, r)
			continue
		}
		svcName := base
		scheme := r.TargetScheme
		if scheme == "" {
			scheme = SchemeHTTP
		}
		lb := traefikLoadBalancer{
			Servers: []traefikServer{{URL: fmt.Sprintf("%s://%s:%d", scheme, r.TargetHost, r.TargetPort)}},
		}
		if scheme == SchemeHTTPS && r.TargetInsecureSkipVerify {
			tName := base + "-transport"
			doc.HTTP.ServersTransports[tName] = traefikServersTransport{InsecureSkipVerify: true}
			lb.ServersTransport = tName
		}
		doc.HTTP.Services[svcName] = traefikService{LoadBalancer: lb}

		rule := hostRule(r.Domain)
		if p := strings.TrimSpace(r.PathPrefix); p != "" && p != "/" {
			rule += fmt.Sprintf(" && PathPrefix(`%s`)", p)
		}

		var mws []string
		if r.IPAllowList != "" {
			name := base + "-ipallow"
			doc.HTTP.Middlewares[name] = traefikMiddleware{IPAllowList: &traefikIPAllowList{SourceRange: SplitCSV(r.IPAllowList)}}
			mws = append(mws, name)
		}
		if r.BasicAuthUsers != "" {
			name := base + "-auth"
			doc.HTTP.Middlewares[name] = traefikMiddleware{BasicAuth: &traefikBasicAuth{Users: SplitLines(r.BasicAuthUsers)}}
			mws = append(mws, name)
		}

		if r.TLS {
			tls := &traefikTLS{}
			if cfg.ACMEEnabled {
				tls.CertResolver = CertResolverName
			}
			doc.HTTP.Routers[base] = traefikRouter{
				Rule: rule, Service: svcName, EntryPoints: []string{entryPointWebSecure}, Middlewares: mws, TLS: tls,
			}
			// http → https redirect on the plain entrypoint. Kept as a separate
			// router so ACME HTTP-01 (served by Traefik itself, higher priority)
			// still works on `web`.
			redir := base + "-redirect"
			doc.HTTP.Middlewares[redir] = traefikMiddleware{RedirectScheme: &traefikRedirectScheme{Scheme: SchemeHTTPS, Permanent: true}}
			doc.HTTP.Routers[base+"-http"] = traefikRouter{
				Rule: rule, Service: svcName, EntryPoints: []string{entryPointWeb}, Middlewares: []string{redir},
			}
		} else {
			doc.HTTP.Routers[base] = traefikRouter{
				Rule: rule, Service: svcName, EntryPoints: []string{entryPointWeb}, Middlewares: mws,
			}
		}
	}

	renderBlocks(&doc, blocks)

	if doc.TCP != nil && len(doc.TCP.Routers) == 0 {
		doc.TCP = nil
	}
	// No enabled routes: return nil so the materializer REMOVES the file. A file
	// with an empty `http: {}` makes Traefik's file provider log
	// "http cannot be a standalone element" on every reload.
	if len(doc.HTTP.Routers) == 0 && doc.TCP == nil {
		return nil, nil
	}

	// yaml.v2 emits `{}` for empty maps; drop them for a tidy file.
	if len(doc.HTTP.Middlewares) == 0 {
		doc.HTTP.Middlewares = nil
	}
	if len(doc.HTTP.ServersTransports) == 0 {
		doc.HTTP.ServersTransports = nil
	}
	body, err := yaml.Marshal(doc)
	if err != nil {
		return nil, err
	}
	header := "# Generated by node-stats (gateway) — do NOT edit by hand; changes are overwritten.\n" +
		"# Manage routes in the node-stats admin panel → Gateway.\n"
	// Port hint for the Applications-view discovery parser (public URLs with
	// the gateway's actual published ports). Only managed mode knows them.
	if cfg.Mode == ModeManaged {
		hp, sp := cfg.HTTPPort, cfg.HTTPSPort
		if hp == 0 {
			hp = 80
		}
		if sp == 0 {
			sp = 443
		}
		header += fmt.Sprintf("# node-stats-gateway: http_port=%d https_port=%d\n", hp, sp)
	}
	return append([]byte(header), body...), nil
}

// blockRouterPriority puts the deny routers above every route router (Traefik
// otherwise ranks by rule length, and a long Host rule could shadow the block).
const blockRouterPriority = 1_000_000

// renderBlocks emits the blocklist: catch-all routers (one per entrypoint)
// matching the blocked ClientIPs, rejected with 403 by an ipAllowList that can
// never match, so the request dies before any route middleware or upstream.
// Only rendered when at least one route exists — an empty gateway serves
// nothing anyway. TCP passthrough connections from blocked IPs are routed to a
// blackhole (connection reset) since raw TLS can't carry a 403.
func renderBlocks(doc *traefikDynamic, blocks []Block) {
	if len(doc.HTTP.Routers) == 0 {
		return
	}
	now := time.Now().UTC()
	var denied []string
	seen := map[string]struct{}{}
	for _, b := range blocks {
		if b.Expired(now) || b.CIDR == "" {
			continue
		}
		if _, dup := seen[b.CIDR]; dup {
			continue
		}
		seen[b.CIDR] = struct{}{}
		denied = append(denied, b.CIDR)
	}
	if len(denied) == 0 {
		return
	}
	sort.Strings(denied)
	if len(denied) > MaxBlocks {
		denied = denied[:MaxBlocks]
	}
	parts := make([]string, len(denied))
	for i, c := range denied {
		parts[i] = fmt.Sprintf("ClientIP(`%s`)", c)
	}
	rule := strings.Join(parts, " || ")

	deny := namePrefix + "blocked"
	bh := namePrefix + "blackhole"
	doc.HTTP.Middlewares[deny] = traefikMiddleware{IPAllowList: &traefikIPAllowList{SourceRange: []string{"255.255.255.255/32"}}}
	// Never dialed: the middleware rejects first. Port 9 = discard.
	doc.HTTP.Services[bh] = traefikService{LoadBalancer: traefikLoadBalancer{Servers: []traefikServer{{URL: "http://127.0.0.1:9"}}}}
	doc.HTTP.Routers[namePrefix+"blocklist-http"] = traefikRouter{
		Rule: rule, Service: bh, EntryPoints: []string{entryPointWeb},
		Middlewares: []string{deny}, Priority: blockRouterPriority,
	}
	doc.HTTP.Routers[namePrefix+"blocklist-https"] = traefikRouter{
		Rule: rule, Service: bh, EntryPoints: []string{entryPointWebSecure},
		Middlewares: []string{deny}, Priority: blockRouterPriority, TLS: &traefikTLS{},
	}
	if doc.TCP != nil && len(doc.TCP.Routers) > 0 {
		doc.TCP.Services[bh] = traefikTCPService{LoadBalancer: traefikTCPLoadBalancer{
			Servers: []traefikTCPServer{{Address: "127.0.0.1:9"}},
		}}
		doc.TCP.Routers[namePrefix+"blocklist"] = traefikTCPRouter{
			Rule: "HostSNI(`*`) && (" + rule + ")", Service: bh,
			EntryPoints: []string{entryPointWebSecure}, Priority: blockRouterPriority,
			TLS: &traefikTCPTLS{Passthrough: true},
		}
	}
}

// SplitCSV splits a comma list, trimming blanks.
// hostRule builds the HTTP router rule for a domain: Host() for an exact name,
// HostRegexp() for a "*." wildcard (direct subdomains).
func hostRule(domain string) string {
	if strings.HasPrefix(domain, "*.") {
		return fmt.Sprintf("HostRegexp(`^[^.]+\\.%s$`)", regexp.QuoteMeta(domain[2:]))
	}
	return fmt.Sprintf("Host(`%s`)", domain)
}

// hostSNIRule is the TCP-router counterpart of hostRule.
func hostSNIRule(domain string) string {
	if strings.HasPrefix(domain, "*.") {
		return fmt.Sprintf("HostSNIRegexp(`^[^.]+\\.%s$`)", regexp.QuoteMeta(domain[2:]))
	}
	return fmt.Sprintf("HostSNI(`%s`)", domain)
}

// renderPassthrough emits the two halves of a delegation route: a TCP
// passthrough router on websecure (the other proxy terminates TLS itself) and a
// plain HTTP router on web to its http port (ACME HTTP-01 + redirects).
func renderPassthrough(doc *traefikDynamic, base string, r Route) {
	httpPort := r.TargetPort
	if httpPort <= 0 {
		httpPort = 80
	}
	httpsPort := r.TargetHTTPSPort
	if httpsPort <= 0 {
		httpsPort = 443
	}
	if doc.TCP == nil {
		doc.TCP = &traefikTCP{Routers: map[string]traefikTCPRouter{}, Services: map[string]traefikTCPService{}}
	}
	tcpName := base + "-tls"
	doc.TCP.Services[tcpName] = traefikTCPService{LoadBalancer: traefikTCPLoadBalancer{
		Servers: []traefikTCPServer{{Address: fmt.Sprintf("%s:%d", r.TargetHost, httpsPort)}},
	}}
	doc.TCP.Routers[tcpName] = traefikTCPRouter{
		Rule: hostSNIRule(r.Domain), Service: tcpName, EntryPoints: []string{entryPointWebSecure},
		TLS: &traefikTCPTLS{Passthrough: true},
	}
	httpName := base + "-http"
	doc.HTTP.Services[httpName] = traefikService{LoadBalancer: traefikLoadBalancer{
		Servers: []traefikServer{{URL: fmt.Sprintf("http://%s:%d", r.TargetHost, httpPort)}},
	}}
	doc.HTTP.Routers[httpName] = traefikRouter{
		Rule: hostRule(r.Domain), Service: httpName, EntryPoints: []string{entryPointWeb},
	}
}

func SplitCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// SplitLines splits on newlines, trimming blanks.
func SplitLines(s string) []string {
	var out []string
	for _, p := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}
