package gateway

import (
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v2"
)

// Traefik v3 file-provider dynamic configuration shapes (only the subset we
// emit). Field names follow Traefik's YAML exactly.
type traefikDynamic struct {
	HTTP traefikHTTP `yaml:"http"`
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
func Render(cfg Config, routes []Route) ([]byte, error) {
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

		rule := fmt.Sprintf("Host(`%s`)", r.Domain)
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

	// No enabled routes: return nil so the materializer REMOVES the file. A file
	// with an empty `http: {}` makes Traefik's file provider log
	// "http cannot be a standalone element" on every reload.
	if len(doc.HTTP.Routers) == 0 {
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
	return append([]byte(header), body...), nil
}

// SplitCSV splits a comma list, trimming blanks.
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
