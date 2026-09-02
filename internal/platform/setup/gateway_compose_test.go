package setup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildComposeContent_GatewayService(t *testing.T) {
	ds := DesiredState{DBMode: DBModeSQLite, Gateway: &GatewayProvision{
		Enabled: true, HTTPPort: 8080, HTTPSPort: 8443, ACMEEnabled: true, ACMEEmail: "ops@example.com", ACMEStaging: true,
		ReadTimeoutSeconds: 86400, AliasHeadersStrategy: AliasHeadersDelete, EncodedPathPolicy: EncodedPathStrict,
	}}
	out := BuildComposeContent(ds)
	for _, want := range []string{
		"--entrypoints.web.http.aliasHeadersStrategy=delete",
		"--entrypoints.websecure.http.aliasHeadersStrategy=delete",
		"--entrypoints.ping.http.aliasHeadersStrategy=delete",
		"--entrypoints.web.http.encodedCharacters.allowEncodedSlash=false",
		"--entrypoints.web.http.encodedCharacters.allowEncodedNullCharacter=false",
		"--entrypoints.web.http.encodedCharacters.allowEncodedBackSlash=false",
		"--entrypoints.websecure.http.encodedCharacters.allowEncodedPercent=true",
		"--entrypoints.ping.http.encodedCharacters.allowEncodedHash=true",
		"--entrypoints.web.transport.respondingTimeouts.readTimeout=86400s",
		"--entrypoints.websecure.transport.respondingTimeouts.readTimeout=86400s",
		"  traefik:",
		"image: ${NODE_STATS_TRAEFIK_IMAGE:-" + DefaultTraefikImage + "}",
		"--providers.file.directory=/etc/traefik/dynamic",
		"--providers.file.watch=true",
		"--entrypoints.web.address=:80",
		"--entrypoints.websecure.address=:443",
		"--certificatesresolvers.le.acme.email=ops@example.com",
		"--certificatesresolvers.le.acme.httpchallenge.entrypoint=web",
		"acme-staging-v02",
		`- "8080:80"`,
		`- "8443:443"`,
		"./data/docker/traefik/dynamic:/etc/traefik/dynamic:ro",
		"./data/docker/traefik/acme:/letsencrypt",
		"--accesslog=true",
		"NODE_STATS_PROJECT=${COMPOSE_PROJECT_NAME:-node-stats}",
		`["CMD", "traefik", "healthcheck", "--ping", "--ping.entryPoint=ping", "--entrypoints.ping.address=:8082"]`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("compose missing %q\n%s", want, out)
		}
	}
	// The traefik stanza must come before the controller (ordering only
	// matters for readability, but keeps the file stable for drift detection).
	if strings.Index(out, "  traefik:") > strings.Index(out, "  node-stats-controller:") {
		t.Error("traefik should precede the controller stanza")
	}
}

func TestBuildComposeContent_GatewayOmittedWhenDisabled(t *testing.T) {
	for _, ds := range []DesiredState{
		{DBMode: DBModeSQLite},
		{DBMode: DBModeSQLite, Gateway: &GatewayProvision{Enabled: false, HTTPPort: 80}},
	} {
		if out := BuildComposeContent(ds); strings.Contains(out, "traefik") {
			t.Errorf("traefik must not be emitted when gateway disabled:\n%s", out)
		}
	}
}

func TestBuildComposeContent_GatewayDefaultPortsAndNoACME(t *testing.T) {
	out := BuildComposeContent(DesiredState{DBMode: DBModeSQLite, Gateway: &GatewayProvision{Enabled: true}})
	if !strings.Contains(out, `- "80:80"`) || !strings.Contains(out, `- "443:443"`) {
		t.Errorf("default ports missing:\n%s", out)
	}
	if strings.Contains(out, "certificatesresolvers") {
		t.Error("ACME flags emitted without acme_enabled")
	}
}

func TestDesiredState_GatewayRoundTrip(t *testing.T) {
	dir := t.TempDir()
	ds := DesiredState{Generation: 3, DBMode: DBModeSQLite, Gateway: &GatewayProvision{Enabled: true, HTTPPort: 80, HTTPSPort: 443}}
	if err := WriteDesiredState(dir, ds); err != nil {
		t.Fatal(err)
	}
	got, err := ReadDesiredState(dir)
	if err != nil || got == nil || got.Gateway == nil || *got.Gateway != *ds.Gateway {
		t.Fatalf("round-trip: %+v %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(dir, DesiredStateFile)); err != nil {
		t.Fatal(err)
	}
	// Hash must change with the gateway section (the controller keys on it).
	other := ds
	other.Gateway = &GatewayProvision{Enabled: false}
	if ds.Hash() == other.Hash() {
		t.Error("hash ignores the gateway section")
	}
}

func TestEncodedCharacterOptions(t *testing.T) {
	count := func(policy string, allow bool) int {
		n := 0
		for _, o := range EncodedCharacterOptions(policy) {
			if o.Allow == allow {
				n++
			}
		}
		return n
	}
	if count(EncodedPathPermissive, true) != 7 || count(EncodedPathParanoid, false) != 7 {
		t.Error("permissive must allow all seven, paranoid reject all seven")
	}
	if count(EncodedPathStrict, false) != 3 || count(EncodedPathStrict, true) != 4 {
		t.Errorf("strict must reject exactly slash/backslash/null: %+v", EncodedCharacterOptions(EncodedPathStrict))
	}
	if len(TraefikEntrypointHardeningFlags("web", GatewayProvision{})) != 0 {
		t.Error("no hardening on the provision → no flags (old Traefik stays bootable)")
	}
	if got := len(TraefikEntrypointHardeningFlags("web", GatewayProvision{AliasHeadersStrategy: AliasHeadersReject, EncodedPathPolicy: EncodedPathStrict})); got != 8 {
		t.Errorf("expected 1 + 7 flags, got %d", got)
	}
}
