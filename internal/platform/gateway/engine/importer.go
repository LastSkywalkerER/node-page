package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/log"
	"gopkg.in/yaml.v2"

	"system-stats/internal/platform/gateway"
)

// File-based route import.
//
// An operator (or a migration script — see scripts/npm-to-gateway-import.py)
// drops <data dir>/gateway-import.yml next to the app's data; the watcher on
// every node picks it up, validates each entry with the SAME rules as the API
// and creates/updates the routes through the service (so they replicate over
// Raft like UI edits). The file is then renamed to gateway-import.applied.yml
// (or .failed.yml when it does not even parse) and a gateway-import.result.yml
// lists what happened per entry — nothing is ever imported twice by accident.
//
// Writing into the data dir already means full control of the node (the DB
// lives there), so no extra authentication is involved.
//
// Format (YAML; keys are the API's JSON field names):
//
//	config:                      # optional, partial — merged onto the stored config
//	  docker_networks: [nginxproxymanager]
//	  enabled: true
//	routes:
//	  - domain: grafana.example.com
//	    target_scheme: http
//	    target_host: grafana
//	    target_port: 3000
//	    tls: true
//	    basic_auth: [{user: admin, password: secret}]
//	  - mode: stream
//	    protocol: tcp
//	    listen_port: 25565
//	    target_host: 10.0.0.9
//	    target_port: 25565
//
// Matching: an entry with route_id updates that route; otherwise the same
// (domain + path_prefix) — or for streams (protocol + listen_port) — updates
// the existing route, and anything else is created.
const (
	ImportFileName        = "gateway-import.yml"
	importAppliedFileName = "gateway-import.applied.yml"
	importFailedFileName  = "gateway-import.failed.yml"
	importResultFileName  = "gateway-import.result.yml"
	importPollInterval    = 5 * time.Second
)

// ImportFile is the parsed import document (either format). A native document
// may embed a Traefik dynamic config under `traefik:` so one file carries the
// config patch AND the routes in Traefik shape.
type ImportFile struct {
	Config   *ImportConfigPatch `json:"config"`
	Routes   []ImportRoute      `json:"routes"`
	Traefik  json.RawMessage    `json:"traefik"`
	Format   string             `json:"-"` // "traefik" | "native"
	Warnings []string           `json:"-"`
}

// ImportConfigPatch is the subset of the gateway config an import may set.
type ImportConfigPatch struct {
	Enabled        *bool    `json:"enabled"`
	DockerNetworks []string `json:"docker_networks"`
	ACMEEnabled    *bool    `json:"acme_enabled"`
	ACMEEmail      string   `json:"acme_email"`
}

// ImportRoute is one entry: a RouteRequest plus an optional route_id to update.
type ImportRoute struct {
	RouteID string `json:"route_id"`
	RouteRequest
}

// ImportResult is what gets written to gateway-import.result.yml and
// returned by POST /gateway/import.
type ImportResult struct {
	At       string              `json:"at"`
	Source   string              `json:"source,omitempty"`
	Format   string              `json:"format,omitempty"`
	DryRun   bool                `json:"dry_run,omitempty"`
	OK       bool                `json:"ok"`
	Error    string              `json:"error,omitempty"`
	Warnings []string            `json:"warnings,omitempty"`
	Config   string              `json:"config,omitempty"`
	Routes   []ImportRouteResult `json:"routes,omitempty"`
	Created  int                 `json:"created"`
	Updated  int                 `json:"updated"`
	Failed   int                 `json:"failed"`
}

// ImportRouteResult is one entry's outcome (in a dry run: what WOULD happen).
type ImportRouteResult struct {
	Index   int    `json:"index"`
	Label   string `json:"label"`
	Action  string `json:"action"` // created | updated | failed
	RouteID string `json:"route_id,omitempty"`
	Error   string `json:"error,omitempty"`
	// Request echoes the parsed entry so a preview can show it.
	Request *RouteRequest `json:"request,omitempty"`
}

// RunImportWatcher polls the data dir for ImportFileName and applies it.
func RunImportWatcher(ctx context.Context, logger *log.Logger, svc Service, dataDir string) {
	if dataDir == "" {
		return
	}
	t := time.NewTicker(importPollInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		path := filepath.Join(dataDir, ImportFileName)
		if _, err := os.Stat(path); err != nil {
			continue
		}
		res := ApplyImportFile(ctx, svc, path)
		if logger != nil {
			if res.OK {
				logger.Info("gateway: import applied", "file", path, "created", res.Created, "updated", res.Updated, "failed", res.Failed)
			} else {
				logger.Error("gateway: import failed", "file", path, "error", res.Error)
			}
		}
	}
}

// ApplyImportFile parses and applies one import file, renames it and writes
// the result file next to it. Never returns an error — everything lands in
// the result (an unreadable/unparsable file is renamed to .failed.yml).
func ApplyImportFile(ctx context.Context, svc Service, path string) ImportResult {
	dir := filepath.Dir(path)
	res := ImportResult{At: time.Now().UTC().Format(time.RFC3339), Source: filepath.Base(path)}
	raw, err := os.ReadFile(path)
	if err != nil {
		res.Error = "read: " + err.Error()
		finishImport(dir, path, res, false)
		return res
	}
	doc, err := ParseImport(raw)
	if err != nil {
		res.Error = "parse: " + err.Error()
		finishImport(dir, path, res, false)
		return res
	}
	res.Format, res.Warnings = doc.Format, doc.Warnings
	res = applyImport(ctx, svc, doc, res, false)
	finishImport(dir, path, res, true)
	return res
}

// Import is the API entry (POST /gateway/import): parse, then apply or preview.
func (s *service) Import(ctx context.Context, raw []byte, dryRun bool) (*ImportResult, error) {
	res := ImportResult{At: time.Now().UTC().Format(time.RFC3339), DryRun: dryRun}
	doc, err := ParseImport(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrValidation, err)
	}
	res.Format, res.Warnings = doc.Format, doc.Warnings
	res = applyImport(ctx, s, doc, res, dryRun)
	return &res, nil
}

// ParseImport decodes an import document. A Traefik dynamic config (top-level
// http/tcp/udp) goes through gateway.ParseDynamic; a native document
// (top-level routes/config, keys = the API's JSON names) through a JSON
// round-trip (yaml.v2 knows nothing about json tags).
func ParseImport(raw []byte) (*ImportFile, error) {
	var any interface{}
	if err := yaml.Unmarshal(raw, &any); err != nil {
		return nil, err
	}
	top, _ := any.(map[interface{}]interface{})
	if top == nil {
		return nil, errors.New("expected a YAML mapping")
	}
	_, hasHTTP := top["http"]
	_, hasTCP := top["tcp"]
	_, hasUDP := top["udp"]
	if hasHTTP || hasTCP || hasUDP {
		routes, warnings, err := gateway.ParseDynamic(raw)
		if err != nil {
			return nil, err
		}
		doc := &ImportFile{Format: "traefik", Warnings: warnings}
		for _, r := range routes {
			doc.Routes = append(doc.Routes, ImportRoute{RouteID: r.RouteID, RouteRequest: RequestFromRoute(r)})
		}
		if len(doc.Routes) == 0 {
			return nil, errors.New("no importable routes: " + strings.Join(warnings, "; "))
		}
		return doc, nil
	}
	j, err := json.Marshal(yamlToJSON(any))
	if err != nil {
		return nil, err
	}
	var doc ImportFile
	dec := json.NewDecoder(strings.NewReader(string(j)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&doc); err != nil {
		return nil, err
	}
	if len(doc.Traefik) > 0 && string(doc.Traefik) != "null" {
		// Embedded Traefik document: marshal back to YAML for the parser.
		var tree interface{}
		if err := json.Unmarshal(doc.Traefik, &tree); err != nil {
			return nil, err
		}
		y, err := yaml.Marshal(tree)
		if err != nil {
			return nil, err
		}
		routes, warnings, err := gateway.ParseDynamic(y)
		if err != nil {
			return nil, fmt.Errorf("traefik section: %w", err)
		}
		doc.Warnings = append(doc.Warnings, warnings...)
		for _, r := range routes {
			doc.Routes = append(doc.Routes, ImportRoute{RouteID: r.RouteID, RouteRequest: RequestFromRoute(r)})
		}
		doc.Traefik = nil
	}
	if doc.Config == nil && len(doc.Routes) == 0 {
		return nil, errors.New("nothing to import (no routes, no config)")
	}
	doc.Format = "native"
	return &doc, nil
}

// RequestFromRoute turns a parsed/stored route back into the API request that
// recreates it (basic-auth users carry their existing hashes).
func RequestFromRoute(r gateway.Route) RouteRequest {
	req := RouteRequest{
		Name: r.Name, Domain: r.Domain, PathPrefix: r.PathPrefix, TargetScheme: r.TargetScheme, TargetHost: r.TargetHost, TargetPort: r.TargetPort,
		TargetHostMAC: r.TargetHostMAC, TargetLabel: r.TargetLabel, TargetInsecureSkipVerify: r.TargetInsecureSkipVerify, TLS: r.TLS, Mode: r.Mode,
		TargetHTTPSPort: r.TargetHTTPSPort, IPAllowList: r.IPAllowList, MaxConnsPerIP: r.MaxConnsPerIP, RateLimitRPS: r.RateLimitRPS, ReadOnly: r.ReadOnly,
		UpstreamTimeoutSeconds: r.UpstreamTimeoutSeconds, MaxBodyBytes: r.MaxBodyBytes,
		Aliases: r.Aliases, StripPrefix: r.StripPrefix, AddPrefix: r.AddPrefix, HostHeaderMode: r.HostHeaderMode, HostHeaderValue: r.HostHeaderValue,
		TargetServerName: r.TargetServerName, ExtraTargets: r.ExtraTargets, HealthCheckPath: r.HealthCheckPath, HealthCheckIntervalSeconds: r.HealthCheckIntervalSeconds,
		Sticky: r.Sticky, RetryAttempts: r.RetryAttempts, RequestHeaders: r.RequestHeaders, ResponseHeaders: r.ResponseHeaders,
		ForwardAuthURL: r.ForwardAuthURL, ForwardAuthResponseHeaders: r.ForwardAuthResponseHeaders, ForwardAuthTrustForwardHeader: r.ForwardAuthTrustForwardHeader,
		SecurityHeaders: r.SecurityHeaders, HSTS: r.HSTS, HSTSIncludeSubdomains: r.HSTSIncludeSubdomains, Compress: r.Compress,
		RedirectURL: r.RedirectURL, RedirectPermanent: r.RedirectPermanent, RedirectPreservePath: r.RedirectPreservePath,
		Protocol: r.Protocol, ListenPort: r.ListenPort,
	}
	enabled := r.Enabled
	req.Enabled = &enabled
	for _, line := range gateway.SplitLines(r.BasicAuthUsers) {
		if i := strings.IndexByte(line, ':'); i > 0 {
			req.BasicAuth = append(req.BasicAuth, BasicAuthInput{User: line[:i], Hash: line[i+1:]})
		}
	}
	return req
}

// yamlToJSON converts yaml.v2's map[interface{}]interface{} trees into
// map[string]interface{} so encoding/json accepts them.
func yamlToJSON(v interface{}) interface{} {
	switch t := v.(type) {
	case map[interface{}]interface{}:
		out := make(map[string]interface{}, len(t))
		for k, val := range t {
			out[fmt.Sprint(k)] = yamlToJSON(val)
		}
		return out
	case []interface{}:
		for i := range t {
			t[i] = yamlToJSON(t[i])
		}
		return t
	default:
		return v
	}
}

func applyImport(ctx context.Context, svc Service, doc *ImportFile, res ImportResult, dryRun bool) ImportResult {
	state, err := svc.GetState(ctx)
	if err != nil {
		res.Error = "load state: " + err.Error()
		return res
	}
	if doc.Config != nil && dryRun {
		res.Config = "would apply"
	}
	if doc.Config != nil && !dryRun {
		cfg := state.Config
		if doc.Config.Enabled != nil {
			cfg.Enabled = *doc.Config.Enabled
		}
		if doc.Config.DockerNetworks != nil {
			cfg.DockerNetworks = doc.Config.DockerNetworks
		}
		if doc.Config.ACMEEnabled != nil {
			cfg.ACMEEnabled = *doc.Config.ACMEEnabled
		}
		if doc.Config.ACMEEmail != "" {
			cfg.ACMEEmail = doc.Config.ACMEEmail
		}
		if _, err := svc.SetConfig(ctx, cfg); err != nil {
			res.Config = "failed: " + err.Error()
			res.Failed++
		} else {
			res.Config = "applied"
		}
	}
	existing := state.Routes
	for i, entry := range doc.Routes {
		rr := ImportRouteResult{Index: i, Label: importLabel(entry.RouteRequest)}
		id := strings.TrimSpace(entry.RouteID)
		if id != "" {
			known := false
			for _, e := range existing {
				if e.RouteID == id {
					known = true
				}
			}
			if !known {
				id = "" // an id from another cluster's file: create here instead
			}
		}
		if id == "" {
			if m := matchExisting(existing, entry.RouteRequest); m != nil {
				id = m.RouteID
			}
		}
		if dryRun {
			req := entry.RouteRequest
			rr.Request = &req
			rr.RouteID = id
			if id != "" {
				rr.Action = "updated"
				res.Updated++
			} else {
				rr.Action = "created"
				res.Created++
			}
			res.Routes = append(res.Routes, rr)
			continue
		}
		var view *RouteView
		var err error
		if id != "" {
			view, err = svc.UpdateRoute(ctx, id, entry.RouteRequest)
			rr.Action = "updated"
		} else {
			view, err = svc.CreateRoute(ctx, entry.RouteRequest)
			rr.Action = "created"
		}
		if err != nil {
			rr.Action, rr.Error = "failed", err.Error()
			res.Failed++
		} else {
			rr.RouteID = view.RouteID
			if id != "" {
				res.Updated++
			} else {
				res.Created++
				existing = append(existing, *view)
			}
		}
		res.Routes = append(res.Routes, rr)
	}
	res.OK = res.Failed == 0 && res.Error == ""
	return res
}

// matchExisting finds the route an entry without route_id refers to.
func matchExisting(existing []RouteView, req RouteRequest) *RouteView {
	if strings.EqualFold(strings.TrimSpace(req.Mode), gateway.RouteModeRedirect) {
		domain := strings.ToLower(strings.TrimSpace(req.Domain))
		for i := range existing {
			if existing[i].IsRedirect() && existing[i].Domain == domain {
				return &existing[i]
			}
		}
		return nil
	}
	mode := strings.ToLower(strings.TrimSpace(req.Mode))
	if mode == "" {
		mode = gateway.RouteModeHTTP
	}
	if mode == gateway.RouteModeStream {
		proto := strings.ToLower(strings.TrimSpace(req.Protocol))
		if proto == "" {
			proto = gateway.ProtoTCP
		}
		for i := range existing {
			e := &existing[i]
			if e.IsStream() && e.Protocol == proto && e.ListenPort == req.ListenPort {
				return e
			}
		}
		return nil
	}
	domain := strings.ToLower(strings.TrimSpace(req.Domain))
	want := strings.Join(gateway.SplitCSV(req.PathPrefix), ",")
	if want == "/" {
		want = ""
	}
	for i := range existing {
		e := &existing[i]
		if e.IsStream() || e.Domain != domain {
			continue
		}
		if strings.Join(e.PathPrefixes(), ",") == want {
			return e
		}
	}
	return nil
}

func importLabel(req RouteRequest) string {
	if strings.EqualFold(strings.TrimSpace(req.Mode), gateway.RouteModeStream) {
		proto := strings.ToLower(strings.TrimSpace(req.Protocol))
		if proto == "" {
			proto = gateway.ProtoTCP
		}
		return fmt.Sprintf("%s/%d", proto, req.ListenPort)
	}
	l := strings.TrimSpace(req.Domain)
	if p := strings.TrimSpace(req.PathPrefix); p != "" && p != "/" {
		l += p
	}
	return l
}

// finishImport renames the source (.applied / .failed) and writes the result.
func finishImport(dir, path string, res ImportResult, parsed bool) {
	target := importAppliedFileName
	if !parsed {
		target = importFailedFileName
	}
	_ = os.Rename(path, filepath.Join(dir, target))
	if b, err := yaml.Marshal(resultToYAML(res)); err == nil {
		_ = os.WriteFile(filepath.Join(dir, importResultFileName), b, 0o644)
	}
}

// resultToYAML renders the result with its json names as YAML keys.
func resultToYAML(res ImportResult) interface{} {
	b, _ := json.Marshal(res)
	var any interface{}
	_ = json.Unmarshal(b, &any)
	return any
}
