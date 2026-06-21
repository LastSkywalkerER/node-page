package connectors

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/charmbracelet/log"
)

// fakeRepo is an in-memory connector repository for the bootstrap tests.
type fakeRepo struct {
	rows     []Connector
	upserts  int
	listErr  error
	failNext bool
}

func (r *fakeRepo) List(context.Context) ([]Connector, error) { return r.rows, r.listErr }
func (r *fakeRepo) GetByID(context.Context, uint) (*Connector, error) {
	return nil, nil
}
func (r *fakeRepo) GetByFingerprint(_ context.Context, fp string) (*Connector, error) {
	for i := range r.rows {
		if r.rows[i].Fingerprint == fp {
			c := r.rows[i]
			return &c, nil
		}
	}
	return nil, nil
}
func (r *fakeRepo) Upsert(_ context.Context, c *Connector) error {
	r.upserts++
	r.rows = append(r.rows, *c)
	return nil
}
func (r *fakeRepo) DeleteByFingerprint(context.Context, string) error { return nil }
func (r *fakeRepo) UpdateLocalStatus(context.Context, uint, string, string, time.Time) error {
	return nil
}

// fakeProber returns a fixed probe result (or an error) and records its inputs.
type fakeProber struct {
	res           *ProbeResult
	err           error
	calls         int
	gotEndpoint   string
	gotTokenID    string
	gotSecret     string
	gotSkipVerify bool
}

func (p *fakeProber) Probe(_ context.Context, endpoint, tokenID, secret string, skip bool) (*ProbeResult, error) {
	p.calls++
	p.gotEndpoint, p.gotTokenID, p.gotSecret, p.gotSkipVerify = endpoint, tokenID, secret, skip
	if p.err != nil {
		return nil, p.err
	}
	return p.res, nil
}

func newBootstrapService(repo *fakeRepo, prober *fakeProber) *service {
	return &service{
		logger:        log.New(io.Discard),
		repo:          repo,
		cipher:        NewCipher("test-jwt-secret"),
		proxmoxProber: prober,
	}
}

func TestBootstrapProxmoxFromEnv_CreatesConnector(t *testing.T) {
	t.Setenv(envProxmoxURL, "https://10.0.0.2:8006/")
	t.Setenv(envProxmoxTokenID, "node-stats@pam!monitor")
	t.Setenv(envProxmoxSecret, "abcd-secret")

	repo := &fakeRepo{}
	prober := &fakeProber{res: &ProbeResult{Fingerprint: "pve/homelab", ClusterName: "homelab"}}
	newBootstrapService(repo, prober).BootstrapProxmoxFromEnv(context.Background())

	if repo.upserts != 1 {
		t.Fatalf("expected 1 connector upsert, got %d", repo.upserts)
	}
	got := repo.rows[0]
	if got.Type != TypeProxmox || got.Fingerprint != "pve/homelab" {
		t.Errorf("unexpected connector %+v", got)
	}
	// Endpoint trailing slash is trimmed; skip-verify defaults to true.
	if prober.gotEndpoint != "https://10.0.0.2:8006" {
		t.Errorf("endpoint = %q", prober.gotEndpoint)
	}
	if !prober.gotSkipVerify {
		t.Error("skip TLS verify should default to true")
	}
	if len(got.SecretEnc) == 0 {
		t.Error("secret was not encrypted/stored")
	}
}

func TestBootstrapProxmoxFromEnv_Idempotent(t *testing.T) {
	t.Setenv(envProxmoxURL, "https://10.0.0.2:8006")
	t.Setenv(envProxmoxTokenID, "node-stats@pam!monitor")
	t.Setenv(envProxmoxSecret, "abcd-secret")

	// Existing connector whose secret decrypts under the CURRENT key → healthy,
	// so auto-connect must leave it untouched.
	enc, _ := NewCipher("test-jwt-secret").Encrypt("stored-token")
	repo := &fakeRepo{rows: []Connector{{Type: TypeProxmox, Fingerprint: "pve/existing", SecretEnc: enc}}}
	prober := &fakeProber{res: &ProbeResult{Fingerprint: "pve/homelab"}}
	newBootstrapService(repo, prober).BootstrapProxmoxFromEnv(context.Background())

	if prober.calls != 0 {
		t.Errorf("prober should not be called when a healthy connector already exists (calls=%d)", prober.calls)
	}
	if repo.upserts != 0 {
		t.Errorf("no upsert expected, got %d", repo.upserts)
	}
}

// When the existing connector's secret can't be decrypted (the cluster JWT
// secret rotated), auto-connect must RE-CREATE it from env so it re-encrypts
// under the current key — otherwise the connector is stuck auth_failed forever.
func TestBootstrapProxmoxFromEnv_HealsUndecryptableSecret(t *testing.T) {
	t.Setenv(envProxmoxURL, "https://10.0.0.2:8006")
	t.Setenv(envProxmoxTokenID, "node-stats@pam!monitor")
	t.Setenv(envProxmoxSecret, "abcd-secret")

	// Secret encrypted under an OLD key the current cipher can't decrypt.
	stale, _ := NewCipher("old-rotated-secret").Encrypt("stored-token")
	repo := &fakeRepo{rows: []Connector{{Type: TypeProxmox, Fingerprint: "pve/existing", SecretEnc: stale}}}
	prober := &fakeProber{res: &ProbeResult{Fingerprint: "pve/homelab"}}
	newBootstrapService(repo, prober).BootstrapProxmoxFromEnv(context.Background())

	if prober.calls == 0 {
		t.Error("prober should be called to heal an undecryptable connector")
	}
	if repo.upserts != 1 {
		t.Fatalf("expected 1 re-create upsert to heal, got %d", repo.upserts)
	}
}

func TestBootstrapProxmoxFromEnv_NoEnvIsNoop(t *testing.T) {
	// No NODE_STATS_PROXMOX_* env set → nothing happens.
	repo := &fakeRepo{}
	prober := &fakeProber{res: &ProbeResult{Fingerprint: "pve/x"}}
	newBootstrapService(repo, prober).BootstrapProxmoxFromEnv(context.Background())
	if prober.calls != 0 || repo.upserts != 0 {
		t.Errorf("expected no-op without env, calls=%d upserts=%d", prober.calls, repo.upserts)
	}
}

func TestBootstrapProxmoxFromEnv_Disabled(t *testing.T) {
	t.Setenv(envProxmoxAutoConnect, "0")
	t.Setenv(envProxmoxURL, "https://10.0.0.2:8006")
	t.Setenv(envProxmoxTokenID, "node-stats@pam!monitor")
	t.Setenv(envProxmoxSecret, "abcd-secret")

	repo := &fakeRepo{}
	prober := &fakeProber{res: &ProbeResult{Fingerprint: "pve/x"}}
	newBootstrapService(repo, prober).BootstrapProxmoxFromEnv(context.Background())
	if prober.calls != 0 || repo.upserts != 0 {
		t.Errorf("expected no-op when disabled, calls=%d upserts=%d", prober.calls, repo.upserts)
	}
}
