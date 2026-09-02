package engine

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"gopkg.in/yaml.v2"
	"strings"
	"testing"

	"github.com/charmbracelet/log"

	"system-stats/internal/platform/setup"
)

func TestNativeRenderStatic(t *testing.T) {
	n := newNativeProvisioner(log.New(nil), "/var/lib/node-stats/data")
	out := string(n.renderStatic(setup.GatewayProvision{Enabled: true, HTTPPort: 8080, HTTPSPort: 8443,
		ACMEEnabled: true, ACMEEmail: "ops@example.com", ACMEStaging: true, ReadTimeoutSeconds: 86400}))
	for _, want := range []string{
		`address: ":8080"`, `address: ":8443"`, `address: "127.0.0.1:8082"`,
		"readTimeout: 86400s",
		`directory: "/var/lib/node-stats/data/traefik/dynamic"`, "watch: true",
		`email: "ops@example.com"`, `storage: "/var/lib/node-stats/data/traefik/acme/acme.json"`,
		"entryPoint: web", "acme-staging-v02",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("static config missing %q:\n%s", want, out)
		}
	}
	// Must be well-formed YAML with the timeout nested under the entrypoint.
	var parsed map[string]any
	if err := yaml.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("static config is not valid yaml: %v\n%s", err, out)
	}
	web := parsed["entryPoints"].(map[any]any)["web"].(map[any]any)
	if got := web["transport"].(map[any]any)["respondingTimeouts"].(map[any]any)["readTimeout"]; got != "86400s" {
		t.Errorf("web readTimeout = %v", got)
	}
	t.Logf("STATIC_YAML_BEGIN\n%sSTATIC_YAML_END", out)
	plain := string(n.renderStatic(setup.GatewayProvision{Enabled: true}))
	if strings.Count(plain, "readTimeout: 0s") != 2 {
		t.Errorf("unlimited read timeout must be rendered on both entrypoints:\n%s", plain)
	}
	if !strings.Contains(plain, `address: ":80"`) || !strings.Contains(plain, `address: ":443"`) || strings.Contains(plain, "certificatesResolvers") {
		t.Errorf("defaults/no-acme wrong:\n%s", plain)
	}
	// The dynamic dir the static config points at must be the one the
	// materializer renders into (ManagedDynamicDir).
	m := &Materializer{deps: MaterializerDeps{DataDir: "/var/lib/node-stats/data"}}
	if m.ManagedDynamicDir() != n.dynamicDir() {
		t.Errorf("dynamic dir mismatch: %s vs %s", m.ManagedDynamicDir(), n.dynamicDir())
	}
}

func TestNativeRenderUnit(t *testing.T) {
	n := newNativeProvisioner(log.New(nil), "/var/lib/node-stats/data")
	out := string(n.renderUnit())
	if !strings.Contains(out, "ExecStart=/var/lib/node-stats/data/traefik/bin/traefik --configfile=/var/lib/node-stats/data/traefik/traefik.yml") {
		t.Errorf("unit ExecStart wrong:\n%s", out)
	}
	if !strings.Contains(out, "Restart=always") || !strings.Contains(out, "WantedBy=multi-user.target") {
		t.Errorf("unit incomplete:\n%s", out)
	}
}

func TestExtractFromTarGz(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range map[string]string{"LICENSE.md": "lic", "traefik": "ELF-binary"} {
		_ = tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg})
		_, _ = tw.Write([]byte(body))
	}
	_ = tw.Close()
	_ = gz.Close()
	bin, err := extractFromTarGz(buf.Bytes(), "traefik")
	if err != nil || string(bin) != "ELF-binary" {
		t.Fatalf("extract: %q %v", bin, err)
	}
	if _, err := extractFromTarGz(buf.Bytes(), "nope"); err == nil {
		t.Error("expected error for a missing entry")
	}
}
