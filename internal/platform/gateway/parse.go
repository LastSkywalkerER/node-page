package gateway

import (
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v2"
)

// ParseDynamic is the renderer's inverse: it reads a Traefik file-provider
// dynamic configuration (ours or anyone's — a hand-written one, a converted
// NPM database, another cluster's node-stats.yml) and returns the routes it
// describes, in the shape the route table stores. Whatever the route model
// cannot express is reported in warnings rather than silently dropped.
//
// Recognised: http routers (Host / HostRegexp wildcard / PathPrefix / Method
// rules, entryPoints web|websecure, tls) with their loadBalancer service
// (servers, passHostHeader, healthCheck, sticky, serversTransport) and
// middlewares (ipAllowList, rateLimit, inFlightReq, basicAuth, buffering,
// forwardAuth, headers, stripPrefix, addPrefix, compress, retry,
// redirectRegex → redirect route); the http→https companion router of a TLS
// route is folded into it; tcp routers with tls.passthrough → passthrough
// routes, tcp/udp routers on ns-<proto>-<port> entrypoints → stream routes.
func ParseDynamic(raw []byte) ([]Route, []string, error) {
	var doc traefikDynamic
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, nil, fmt.Errorf("not a Traefik dynamic config: %w", err)
	}
	if len(doc.HTTP.Routers) == 0 && (doc.TCP == nil || len(doc.TCP.Routers) == 0) && (doc.UDP == nil || len(doc.UDP.Routers) == 0) {
		return nil, nil, fmt.Errorf("no routers found (expected http/tcp/udp sections)")
	}
	p := &dynParser{doc: doc}
	p.parseHTTP()
	p.parseTCP()
	p.parseUDP()
	sort.SliceStable(p.routes, func(i, j int) bool {
		if p.routes[i].Domain != p.routes[j].Domain {
			return p.routes[i].Domain < p.routes[j].Domain
		}
		return p.routes[i].PathPrefix < p.routes[j].PathPrefix
	})
	return p.routes, p.warnings, nil
}

type dynParser struct {
	doc      traefikDynamic
	routes   []Route
	warnings []string
}

func (p *dynParser) warn(format string, a ...interface{}) {
	p.warnings = append(p.warnings, fmt.Sprintf(format, a...))
}

var (
	// ruleHostToken matches every host matcher call in rule order (the first
	// hostname is the route's domain, the rest its aliases).
	ruleHostToken  = regexp.MustCompile("(?i)\\b(HostSNIRegexp|HostSNI|HostRegexp|Host)\\(([^)]*)\\)")
	rulePathPrefix = regexp.MustCompile("(?i)\\bPathPrefix\\(`([^`]*)`\\)")
	rulePath       = regexp.MustCompile("(?i)\\bPath\\(`([^`]*)`\\)")
	ruleMethod     = regexp.MustCompile("(?i)\\bMethod\\(`([^`]*)`\\)")
	ruleArg        = regexp.MustCompile("`([^`]*)`")
	// wildcardRegexp is the exact form hostRule emits for "*.domain".
	wildcardRegexp = regexp.MustCompile(`^\^\[\^\.\]\+\\\.(.+)\$$`)
	// entryPointStream matches the stream entrypoints StreamEntryPoint emits.
	entryPointStream = regexp.MustCompile(`^ns-(tcp|udp)-(\d+)$`)
)

// hostsFromRule extracts the hostnames of a rule in order (Host(a) || Host(b),
// Host(a, b), HostRegexp wildcard) — first is the domain, rest aliases. sni
// selects the HostSNI*/Host* family.
func hostsFromRule(rule string, sni bool) (hosts []string, unsupported []string) {
	seen := map[string]bool{}
	add := func(h string) {
		h = strings.ToLower(strings.TrimSpace(h))
		if h != "" && !seen[h] {
			seen[h] = true
			hosts = append(hosts, h)
		}
	}
	for _, m := range ruleHostToken.FindAllStringSubmatch(rule, -1) {
		kind := strings.ToLower(m[1])
		isSNI := strings.HasPrefix(kind, "hostsni")
		if isSNI != sni {
			continue
		}
		if strings.HasSuffix(kind, "regexp") {
			for _, a := range ruleArg.FindAllStringSubmatch(m[2], -1) {
				if w := wildcardRegexp.FindStringSubmatch(a[1]); w != nil {
					add("*." + strings.ReplaceAll(w[1], `\.`, "."))
				} else {
					unsupported = append(unsupported, m[0])
				}
			}
			continue
		}
		for _, a := range ruleArg.FindAllStringSubmatch(m[2], -1) {
			add(a[1])
		}
	}
	return hosts, unsupported
}

func pathPrefixesFromRule(rule string) []string {
	var out []string
	for _, m := range rulePathPrefix.FindAllStringSubmatch(rule, -1) {
		out = append(out, m[1])
	}
	for _, m := range rulePath.FindAllStringSubmatch(rule, -1) {
		out = append(out, m[1]) // exact Path() approximated as a prefix
	}
	return out
}

func parseServerURL(u string) (scheme, host string, port int, ok bool) {
	pu, err := url.Parse(strings.TrimSpace(u))
	if err != nil || pu.Host == "" {
		return "", "", 0, false
	}
	scheme = strings.ToLower(pu.Scheme)
	if !ValidScheme(scheme) {
		return "", "", 0, false
	}
	host = pu.Hostname()
	port, _ = strconv.Atoi(pu.Port())
	if port == 0 {
		port = 80
		if scheme == SchemeHTTPS {
			port = 443
		}
	}
	return scheme, host, port, host != ""
}

func parseSeconds(d string) int {
	d = strings.TrimSpace(d)
	if d == "" {
		return 0
	}
	if n, err := strconv.Atoi(d); err == nil { // bare number = nanoseconds in Traefik; treat as seconds
		return n
	}
	if strings.HasSuffix(d, "ms") {
		return 0
	}
	if strings.HasSuffix(d, "s") {
		n, _ := strconv.Atoi(strings.TrimSuffix(d, "s"))
		return n
	}
	if strings.HasSuffix(d, "m") {
		n, _ := strconv.Atoi(strings.TrimSuffix(d, "m"))
		return n * 60
	}
	if strings.HasSuffix(d, "h") {
		n, _ := strconv.Atoi(strings.TrimSuffix(d, "h"))
		return n * 3600
	}
	return 0
}

// routeIDFromName recovers the id from our own object names (ns-<id>[-suffix])
// so re-importing a node-stats.yml updates the same routes; foreign names → "".
func routeIDFromName(name string) string {
	rest := strings.TrimPrefix(name, namePrefix)
	if rest == name {
		return ""
	}
	if i := strings.IndexByte(rest, '-'); i > 0 {
		rest = rest[:i]
	}
	if len(rest) != 12 {
		return ""
	}
	for _, c := range rest {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return ""
		}
	}
	return rest
}

func (p *dynParser) parseHTTP() {
	// Fold redirect companions: a `web` router whose only middleware is a
	// redirectScheme→https and whose rule matches a websecure router is the
	// http half of a TLS route, not a route of its own.
	redirectOnly := map[string]bool{}
	for name, r := range p.doc.HTTP.Routers {
		if len(r.Middlewares) == 1 {
			if mw, ok := p.doc.HTTP.Middlewares[stripProvider(r.Middlewares[0])]; ok && mw.RedirectScheme != nil && strings.EqualFold(mw.RedirectScheme.Scheme, "https") {
				redirectOnly[name] = true
			}
		}
	}
	names := make([]string, 0, len(p.doc.HTTP.Routers))
	for n := range p.doc.HTTP.Routers {
		names = append(names, n)
	}
	sort.Strings(names)
	// A TLS route rendered by us has ns-<id> (websecure) + ns-<id>-http (web);
	// a redirect route has ns-<id> (web) + ns-<id>-tls (websecure) — dedupe by
	// rule so both halves become one route.
	seenRule := map[string]int{} // rule → index in p.routes
	for _, name := range names {
		r := p.doc.HTTP.Routers[name]
		if redirectOnly[name] {
			// Mark the matching TLS route (if any) as TLS — it already is.
			continue
		}
		hosts, unsupported := hostsFromRule(r.Rule, false)
		for _, u := range unsupported {
			p.warn("router %s: unsupported host matcher %s — skipped", name, u)
		}
		if len(hosts) == 0 {
			p.warn("router %s: no Host() in rule %q — skipped", name, r.Rule)
			continue
		}
		tls := r.TLS != nil
		for _, ep := range r.EntryPoints {
			if ep == entryPointWebSecure {
				tls = true
			}
		}
		if idx, dup := seenRule[r.Rule]; dup {
			if tls {
				p.routes[idx].TLS = true
			}
			continue
		}
		route := Route{RouteID: routeIDFromName(name), Domain: hosts[0], Aliases: strings.Join(hosts[1:], ","), TLS: tls, Enabled: true,
			Mode: RouteModeHTTP, Name: strings.TrimPrefix(name, namePrefix)}
		if route.RouteID != "" {
			route.Name = ""
		}
		route.PathPrefix = strings.Join(pathPrefixesFromRule(r.Rule), ",")
		if ms := ruleMethod.FindAllStringSubmatch(r.Rule, -1); len(ms) > 0 {
			onlyRead := true
			for _, m := range ms {
				switch strings.ToUpper(m[1]) {
				case "GET", "HEAD", "OPTIONS":
				default:
					onlyRead = false
				}
			}
			route.ReadOnly = onlyRead
		}
		// Middlewares.
		isRedirect := false
		for _, mwName := range r.Middlewares {
			mw, ok := p.doc.HTTP.Middlewares[stripProvider(mwName)]
			if !ok {
				p.warn("router %s: middleware %s not defined in this file — ignored", name, mwName)
				continue
			}
			switch {
			case mw.RedirectRegex != nil:
				isRedirect = true
				route.Mode = RouteModeRedirect
				route.RedirectPermanent = mw.RedirectRegex.Permanent
				repl := mw.RedirectRegex.Replacement
				if strings.HasSuffix(repl, "${1}") {
					route.RedirectPreservePath = true
					repl = strings.TrimSuffix(repl, "${1}")
				}
				route.RedirectURL = repl
			case mw.RedirectScheme != nil:
				// http→https on a non-companion router: the route is TLS.
				route.TLS = true
			case mw.IPAllowList != nil:
				route.IPAllowList = strings.Join(mw.IPAllowList.SourceRange, ",")
			case mw.RateLimit != nil:
				route.RateLimitRPS = mw.RateLimit.Average
			case mw.InFlightReq != nil:
				route.MaxConnsPerIP = mw.InFlightReq.Amount
			case mw.BasicAuth != nil:
				route.BasicAuthUsers = strings.Join(mw.BasicAuth.Users, "\n")
			case mw.Buffering != nil:
				route.MaxBodyBytes = mw.Buffering.MaxRequestBodyBytes
			case mw.ForwardAuth != nil:
				route.ForwardAuthURL = mw.ForwardAuth.Address
				route.ForwardAuthResponseHeaders = strings.Join(mw.ForwardAuth.AuthResponseHeaders, ",")
				route.ForwardAuthTrustForwardHeader = mw.ForwardAuth.TrustForwardHeader
			case mw.Headers != nil:
				p.applyHeaders(&route, mw.Headers)
			case mw.StripPrefix != nil:
				route.StripPrefix = true
				if route.PathPrefix == "" {
					route.PathPrefix = strings.Join(mw.StripPrefix.Prefixes, ",")
				}
			case mw.AddPrefix != nil:
				route.AddPrefix = mw.AddPrefix.Prefix
			case mw.Compress != nil:
				route.Compress = true
			case mw.Retry != nil:
				route.RetryAttempts = mw.Retry.Attempts
			default:
				p.warn("router %s: middleware %s has no equivalent in the route model — dropped", name, mwName)
			}
		}
		if isRedirect {
			if route.RedirectURL == "" {
				p.warn("router %s: redirect without a replacement URL — skipped", name)
				continue
			}
			route.TargetScheme, route.TargetHost, route.TargetPort = SchemeHTTP, "redirect", 80
			seenRule[r.Rule] = len(p.routes)
			p.routes = append(p.routes, route)
			continue
		}
		// Service → target(s).
		svcName := stripProvider(r.Service)
		svc, ok := p.doc.HTTP.Services[svcName]
		if !ok {
			if r.Service == noopService {
				p.warn("router %s: noop service without a redirect — skipped", name)
			} else {
				p.warn("router %s: service %s not defined in this file — skipped", name, r.Service)
			}
			continue
		}
		lb := svc.LoadBalancer
		if len(lb.Servers) == 0 {
			p.warn("router %s: service %s has no servers — skipped", name, svcName)
			continue
		}
		scheme, host, port, ok := parseServerURL(lb.Servers[0].URL)
		if !ok {
			p.warn("router %s: server url %q not understood — skipped", name, lb.Servers[0].URL)
			continue
		}
		route.TargetScheme, route.TargetHost, route.TargetPort = scheme, host, port
		var extra []string
		for _, s := range lb.Servers[1:] {
			sc, h, pt, ok := parseServerURL(s.URL)
			if !ok {
				p.warn("router %s: extra server %q not understood — dropped", name, s.URL)
				continue
			}
			if sc != scheme {
				p.warn("router %s: extra server %q uses another scheme — dropped", name, s.URL)
				continue
			}
			extra = append(extra, fmt.Sprintf("%s:%d", h, pt))
		}
		route.ExtraTargets = strings.Join(extra, "\n")
		if lb.PassHostHeader != nil && !*lb.PassHostHeader {
			route.HostHeaderMode = HostHeaderUpstream
		}
		if lb.HealthCheck != nil && lb.HealthCheck.Path != "" {
			route.HealthCheckPath = lb.HealthCheck.Path
			route.HealthCheckIntervalSeconds = parseSeconds(lb.HealthCheck.Interval)
		}
		route.Sticky = lb.Sticky != nil
		if lb.ServersTransport != "" {
			if tr, ok := p.doc.HTTP.ServersTransports[stripProvider(lb.ServersTransport)]; ok {
				route.TargetInsecureSkipVerify = tr.InsecureSkipVerify && scheme == SchemeHTTPS
				if scheme == SchemeHTTPS {
					route.TargetServerName = tr.ServerName
				}
				if tr.ForwardingTimeouts != nil {
					route.UpstreamTimeoutSeconds = parseSeconds(tr.ForwardingTimeouts.ResponseHeaderTimeout)
				}
			} else {
				p.warn("router %s: serversTransport %s not defined in this file — ignored", name, lb.ServersTransport)
			}
		}
		seenRule[r.Rule] = len(p.routes)
		p.routes = append(p.routes, route)
	}
}

// applyHeaders maps a headers middleware onto the route's header fields.
func (p *dynParser) applyHeaders(route *Route, h *traefikHeaders) {
	var req, resp []string
	keys := make([]string, 0, len(h.CustomRequestHeaders))
	for k := range h.CustomRequestHeaders {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if strings.EqualFold(k, "Host") {
			route.HostHeaderMode, route.HostHeaderValue = HostHeaderCustom, h.CustomRequestHeaders[k]
			continue
		}
		req = append(req, k+": "+h.CustomRequestHeaders[k])
	}
	keys = keys[:0]
	for k := range h.CustomResponseHeaders {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		resp = append(resp, k+": "+h.CustomResponseHeaders[k])
	}
	route.RequestHeaders = strings.Join(req, "\n")
	route.ResponseHeaders = strings.Join(resp, "\n")
	if h.CustomFrameOptionsValue != "" || h.ContentTypeNosniff || h.BrowserXSSFilter || h.ReferrerPolicy != "" {
		route.SecurityHeaders = true
	}
	if h.STSSeconds > 0 {
		route.HSTS = true
		route.HSTSIncludeSubdomains = h.STSIncludeSubdomains
	}
}

func (p *dynParser) parseTCP() {
	if p.doc.TCP == nil {
		return
	}
	names := make([]string, 0, len(p.doc.TCP.Routers))
	for n := range p.doc.TCP.Routers {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		r := p.doc.TCP.Routers[name]
		svc, ok := p.doc.TCP.Services[stripProvider(r.Service)]
		if !ok || len(svc.LoadBalancer.Servers) == 0 {
			p.warn("tcp router %s: service %s has no servers — skipped", name, r.Service)
			continue
		}
		addrs := make([]string, 0, len(svc.LoadBalancer.Servers))
		for _, s := range svc.LoadBalancer.Servers {
			addrs = append(addrs, s.Address)
		}
		host, port, ok := splitHostPort(addrs[0])
		if !ok {
			p.warn("tcp router %s: address %q not understood — skipped", name, addrs[0])
			continue
		}
		// Stream: our own ns-<proto>-<port> entrypoint, or any non-web entrypoint
		// with a catch-all HostSNI(`*`).
		var streamPort int
		for _, ep := range r.EntryPoints {
			if m := entryPointStream.FindStringSubmatch(ep); m != nil && m[1] == ProtoTCP {
				streamPort, _ = strconv.Atoi(m[2])
			}
		}
		if streamPort > 0 || (r.TLS == nil && strings.Contains(r.Rule, "HostSNI(`*`)")) {
			if streamPort == 0 {
				p.warn("tcp router %s: catch-all on a non node-stats entrypoint — set its public port after import", name)
			}
			route := Route{RouteID: routeIDFromName(name), Mode: RouteModeStream, Protocol: ProtoTCP, ListenPort: streamPort,
				TargetScheme: ProtoTCP, TargetHost: host, TargetPort: port, ExtraTargets: strings.Join(addrs[1:], "\n"), Enabled: true}
			if route.RouteID == "" {
				route.Name = name
			}
			p.routes = append(p.routes, route)
			continue
		}
		if r.TLS == nil || !r.TLS.Passthrough {
			p.warn("tcp router %s: TLS-terminating tcp routers have no equivalent — skipped", name)
			continue
		}
		hosts, unsupported := hostsFromRule(r.Rule, true)
		for _, u := range unsupported {
			p.warn("tcp router %s: unsupported SNI matcher %s — skipped", name, u)
		}
		if len(hosts) == 0 {
			p.warn("tcp router %s: no HostSNI() in rule %q — skipped", name, r.Rule)
			continue
		}
		route := Route{RouteID: routeIDFromName(name), Mode: RouteModePassthrough, Domain: hosts[0], Aliases: strings.Join(hosts[1:], ","),
			TargetScheme: SchemeHTTP, TargetHost: host, TargetHTTPSPort: port, TargetPort: 80, Enabled: true}
		if route.RouteID == "" {
			route.Name = strings.TrimSuffix(name, "-tls")
		}
		// Our passthrough renders a companion http router ns-<id>-http → :80 of
		// the same host; pick its port up when present.
		if hr, ok := p.doc.HTTP.Routers[strings.TrimSuffix(name, "-tls")+"-http"]; ok {
			if hs, ok := p.doc.HTTP.Services[stripProvider(hr.Service)]; ok && len(hs.LoadBalancer.Servers) > 0 {
				if _, _, hp, ok := parseServerURL(hs.LoadBalancer.Servers[0].URL); ok {
					route.TargetPort = hp
				}
			}
		}
		p.routes = append(p.routes, route)
	}
}

func (p *dynParser) parseUDP() {
	if p.doc.UDP == nil {
		return
	}
	names := make([]string, 0, len(p.doc.UDP.Routers))
	for n := range p.doc.UDP.Routers {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		r := p.doc.UDP.Routers[name]
		svc, ok := p.doc.UDP.Services[stripProvider(r.Service)]
		if !ok || len(svc.LoadBalancer.Servers) == 0 {
			p.warn("udp router %s: service %s has no servers — skipped", name, r.Service)
			continue
		}
		addrs := make([]string, 0, len(svc.LoadBalancer.Servers))
		for _, s := range svc.LoadBalancer.Servers {
			addrs = append(addrs, s.Address)
		}
		host, port, ok := splitHostPort(addrs[0])
		if !ok {
			p.warn("udp router %s: address %q not understood — skipped", name, addrs[0])
			continue
		}
		var streamPort int
		for _, ep := range r.EntryPoints {
			if m := entryPointStream.FindStringSubmatch(ep); m != nil && m[1] == ProtoUDP {
				streamPort, _ = strconv.Atoi(m[2])
			}
		}
		if streamPort == 0 {
			p.warn("udp router %s: not on a node-stats entrypoint — set its public port after import", name)
		}
		route := Route{RouteID: routeIDFromName(name), Mode: RouteModeStream, Protocol: ProtoUDP, ListenPort: streamPort,
			TargetScheme: ProtoUDP, TargetHost: host, TargetPort: port, ExtraTargets: strings.Join(addrs[1:], "\n"), Enabled: true}
		if route.RouteID == "" {
			route.Name = name
		}
		p.routes = append(p.routes, route)
	}
}

func splitHostPort(addr string) (string, int, bool) {
	i := strings.LastIndexByte(addr, ':')
	if i <= 0 {
		return "", 0, false
	}
	port, err := strconv.Atoi(addr[i+1:])
	if err != nil || port <= 0 {
		return "", 0, false
	}
	return strings.Trim(addr[:i], "[]"), port, true
}

// stripProvider drops a "@file"/"@docker" provider suffix from a reference.
func stripProvider(ref string) string {
	if i := strings.IndexByte(ref, '@'); i > 0 {
		return ref[:i]
	}
	return ref
}
