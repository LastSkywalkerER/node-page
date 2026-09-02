package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/log"

	"system-stats/internal/platform/gateway"
)

const importDoc = `
config:
  docker_networks: [nginxproxymanager, netbird_netbird]
routes:
  - name: Dashboard
    domain: dashboard.example.com
    target_scheme: http
    target_host: node-stats
    target_port: 9090
    tls: true
    hsts: true
    max_conns_per_ip: 100
  - domain: nb.example.com
    path_prefix: /signalexchange.SignalExchange,/management.ManagementService
    target_scheme: h2c
    target_host: netbird-server
    target_port: 80
    tls: true
  - mode: stream
    protocol: udp
    listen_port: 64738
    target_host: 10.0.0.9
    target_port: 64738
  - domain: broken host
    target_host: h
    target_port: 80
`

func TestImport_ParseApplyAndRerunUpdates(t *testing.T) {
	repo := newFakeRepo()
	store := &fakeStore{}
	svc := NewService(log.New(nil), repo, nil, store, nil, nil, nil, nil)
	ctx := context.Background()
	if _, err := svc.SetConfig(ctx, gateway.Config{Enabled: true, NodeMAC: "AA:BB"}); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, ImportFileName)
	if err := os.WriteFile(path, []byte(importDoc), 0o644); err != nil {
		t.Fatal(err)
	}
	res := ApplyImportFile(ctx, svc, path)
	if res.OK || res.Created != 3 || res.Failed != 1 || res.Config != "applied" {
		t.Fatalf("first import: %+v", res)
	}
	if res.Routes[3].Action != "failed" || !strings.Contains(res.Routes[3].Error, "domain") || res.Routes[2].Label != "udp/64738" {
		t.Errorf("per-entry results: %+v", res.Routes)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("source file must be renamed away")
	}
	if _, err := os.Stat(filepath.Join(dir, importAppliedFileName)); err != nil {
		t.Error("applied copy missing")
	}
	if b, err := os.ReadFile(filepath.Join(dir, importResultFileName)); err != nil || !strings.Contains(string(b), "created: 3") {
		t.Errorf("result file: %v %s", err, b)
	}
	cfg, _ := LoadConfig(ctx, store)
	if len(cfg.DockerNetworks) != 2 || !cfg.Enabled {
		t.Errorf("config patch: %+v", cfg)
	}
	state, _ := svc.GetState(ctx)
	if len(state.Routes) != 3 {
		t.Fatalf("routes: %d", len(state.Routes))
	}

	// Re-running the same file (with one field changed) UPDATES by domain+prefix
	// / protocol+port instead of duplicating, and stays idempotent otherwise.
	again := strings.Replace(importDoc, "target_port: 9090", "target_port: 9091", 1)
	again = strings.Replace(again, "  - domain: broken host\n    target_host: h\n    target_port: 80\n", "", 1)
	if err := os.WriteFile(path, []byte(again), 0o644); err != nil {
		t.Fatal(err)
	}
	res = ApplyImportFile(ctx, svc, path)
	if !res.OK || res.Updated != 3 || res.Created != 0 {
		t.Fatalf("second import: %+v", res)
	}
	state, _ = svc.GetState(ctx)
	if len(state.Routes) != 3 {
		t.Fatalf("routes after rerun: %d", len(state.Routes))
	}
	for _, r := range state.Routes {
		if r.Domain == "dashboard.example.com" && r.TargetPort != 9091 {
			t.Errorf("update not applied: %+v", r.Route)
		}
	}

	// Native wrapper with an embedded Traefik document + config patch.
	wrapped := "config:\n  docker_networks: [npm]\ntraefik:\n  http:\n    routers:\n      g:\n        rule: Host(`g.example.com`)\n        entryPoints: [websecure]\n        service: g\n        tls: {}\n    services:\n      g:\n        loadBalancer:\n          servers:\n            - url: http://grafana:3000\n"
	if err := os.WriteFile(path, []byte(wrapped), 0o644); err != nil {
		t.Fatal(err)
	}
	res = ApplyImportFile(ctx, svc, path)
	if !res.OK || res.Created != 1 || res.Config != "applied" || res.Format != "native" {
		t.Fatalf("wrapped import: %+v", res)
	}
	cfg, _ = LoadConfig(ctx, store)
	if len(cfg.DockerNetworks) != 1 || cfg.DockerNetworks[0] != "npm" {
		t.Errorf("wrapped config: %+v", cfg.DockerNetworks)
	}

	// Garbage → .failed.yml + result with the parse error; unknown keys rejected.
	if err := os.WriteFile(path, []byte("routes:\n  - domian: typo.example.com\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res = ApplyImportFile(ctx, svc, path)
	if res.OK || !strings.Contains(res.Error, "unknown field") {
		t.Fatalf("typo must be rejected: %+v", res)
	}
	if _, err := os.Stat(filepath.Join(dir, importFailedFileName)); err != nil {
		t.Error("failed copy missing")
	}
}
