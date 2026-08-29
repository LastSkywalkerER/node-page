package engine

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/log"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"

	"system-stats/internal/platform/gateway"
)

type fakeRepo struct{ rows map[string]gateway.Route }

func newFakeRepo() *fakeRepo { return &fakeRepo{rows: map[string]gateway.Route{}} }

func (f *fakeRepo) List(context.Context) ([]gateway.Route, error) {
	out := make([]gateway.Route, 0, len(f.rows))
	for _, r := range f.rows {
		out = append(out, r)
	}
	return out, nil
}
func (f *fakeRepo) GetByRouteID(_ context.Context, id string) (*gateway.Route, error) {
	r, ok := f.rows[id]
	if !ok {
		return nil, gorm.ErrRecordNotFound
	}
	return &r, nil
}
func (f *fakeRepo) Upsert(_ context.Context, r *gateway.Route) error {
	f.rows[r.RouteID] = *r
	return nil
}
func (f *fakeRepo) DeleteByRouteID(_ context.Context, id string) error {
	delete(f.rows, id)
	return nil
}

type fakeStore struct{ v string }

func (s *fakeStore) Get(context.Context) (string, error)   { return s.v, nil }
func (s *fakeStore) Set(_ context.Context, v string) error { s.v = v; return nil }

type fakeRaft struct {
	enabled  bool
	upserts  []gateway.Route
	deletes  []string
	applyTo  *fakeRepo // simulates the applier landing the row locally
	failNext error
}

func (r *fakeRaft) Enabled() bool { return r.enabled }
func (r *fakeRaft) SubmitGatewayRouteUpsert(ctx context.Context, g gateway.Route) error {
	if r.failNext != nil {
		return r.failNext
	}
	r.upserts = append(r.upserts, g)
	if r.applyTo != nil {
		_ = r.applyTo.Upsert(ctx, &g)
	}
	return nil
}
func (r *fakeRaft) SubmitGatewayRouteDelete(ctx context.Context, id string) error {
	r.deletes = append(r.deletes, id)
	if r.applyTo != nil {
		_ = r.applyTo.DeleteByRouteID(ctx, id)
	}
	return nil
}

func newSvc(repo *fakeRepo, raft *fakeRaft) Service {
	var repl Replicator
	if raft != nil {
		repl = raft
	}
	return NewService(log.New(nil), repo, &fakeStore{}, nil, nil, repl, nil)
}

func TestCreateRoute_StandaloneWritesRepoAndHashesPassword(t *testing.T) {
	repo := newFakeRepo()
	svc := newSvc(repo, nil)
	v, err := svc.CreateRoute(context.Background(), RouteRequest{
		Domain: "Grafana.Example.COM", TargetHost: "10.0.0.5", TargetPort: 3000, TLS: true,
		BasicAuth:   []BasicAuthInput{{User: "admin", Password: "s3cret"}},
		IPAllowList: "10.0.0.0/8, 1.2.3.4",
	})
	if err != nil {
		t.Fatal(err)
	}
	if v.Domain != "grafana.example.com" || v.TargetScheme != "http" || !v.Enabled || v.Name != "grafana.example.com" {
		t.Errorf("normalisation: %+v", v.Route)
	}
	if len(v.BasicAuthUsers) != 1 || v.BasicAuthUsers[0] != "admin" || !v.Protected {
		t.Errorf("view users: %+v protected=%v", v.BasicAuthUsers, v.Protected)
	}
	stored := repo.rows[v.RouteID]
	line := stored.BasicAuthUsers
	if !strings.HasPrefix(line, "admin:$2a$") {
		t.Fatalf("expected bcrypt htpasswd line, got %q", line)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(strings.TrimPrefix(line, "admin:")), []byte("s3cret")); err != nil {
		t.Error("hash does not verify")
	}
	if stored.IPAllowList != "10.0.0.0/8,1.2.3.4" {
		t.Errorf("allow list: %q", stored.IPAllowList)
	}
}

func TestCreateRoute_Validation(t *testing.T) {
	svc := newSvc(newFakeRepo(), nil)
	cases := []RouteRequest{
		{Domain: "", TargetHost: "h", TargetPort: 80},
		{Domain: "bad host", TargetHost: "h", TargetPort: 80},
		{Domain: "ok.example.com", TargetHost: "", TargetPort: 80},
		{Domain: "ok.example.com", TargetHost: "h", TargetPort: 70000},
		{Domain: "ok.example.com", TargetHost: "h", TargetPort: 80, PathPrefix: "noslash"},
		{Domain: "ok.example.com", TargetHost: "h", TargetPort: 80, IPAllowList: "not-a-cidr"},
		{Domain: "ok.example.com", TargetHost: "h", TargetPort: 80, BasicAuth: []BasicAuthInput{{User: "u", Password: ""}}},
		{Domain: "ok.example.com", TargetHost: "h", TargetPort: 80, TargetScheme: "ftp"},
	}
	for i, c := range cases {
		if _, err := svc.CreateRoute(context.Background(), c); !errors.Is(err, ErrValidation) {
			t.Errorf("case %d: expected validation error, got %v", i, err)
		}
	}
}

func TestCreateRoute_DomainConflict(t *testing.T) {
	svc := newSvc(newFakeRepo(), nil)
	req := RouteRequest{Domain: "a.example.com", TargetHost: "h", TargetPort: 80}
	if _, err := svc.CreateRoute(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateRoute(context.Background(), req); !errors.Is(err, ErrValidation) {
		t.Fatalf("duplicate domain accepted: %v", err)
	}
	req.PathPrefix = "/other"
	if _, err := svc.CreateRoute(context.Background(), req); err != nil {
		t.Fatalf("different path should be allowed: %v", err)
	}
}

func TestRoutes_GoThroughRaftWhenEnabled(t *testing.T) {
	repo := newFakeRepo()
	raft := &fakeRaft{enabled: true, applyTo: repo}
	svc := newSvc(repo, raft)
	v, err := svc.CreateRoute(context.Background(), RouteRequest{Domain: "a.example.com", TargetHost: "h", TargetPort: 80})
	if err != nil {
		t.Fatal(err)
	}
	if len(raft.upserts) != 1 || raft.upserts[0].RouteID != v.RouteID {
		t.Fatalf("expected a raft upsert, got %+v", raft.upserts)
	}
	off := false
	if _, err := svc.UpdateRoute(context.Background(), v.RouteID, RouteRequest{Domain: "a.example.com", TargetHost: "h", TargetPort: 81, Enabled: &off}); err != nil {
		t.Fatal(err)
	}
	if got := repo.rows[v.RouteID]; got.TargetPort != 81 || got.Enabled {
		t.Errorf("update not applied: %+v", got)
	}
	if err := svc.DeleteRoute(context.Background(), v.RouteID); err != nil {
		t.Fatal(err)
	}
	if len(raft.deletes) != 1 || len(repo.rows) != 0 {
		t.Errorf("delete: raft=%v rows=%d", raft.deletes, len(repo.rows))
	}
	// Raft error must surface, not be swallowed by a local write.
	raft.failNext = errors.New("no leader")
	if _, err := svc.CreateRoute(context.Background(), RouteRequest{Domain: "b.example.com", TargetHost: "h", TargetPort: 80}); err == nil || len(repo.rows) != 0 {
		t.Errorf("raft failure swallowed: err=%v rows=%d", err, len(repo.rows))
	}
}

func TestUpdateRoute_BlankPasswordKeepsHash(t *testing.T) {
	repo := newFakeRepo()
	svc := newSvc(repo, nil)
	v, err := svc.CreateRoute(context.Background(), RouteRequest{Domain: "a.example.com", TargetHost: "h", TargetPort: 80,
		BasicAuth: []BasicAuthInput{{User: "admin", Password: "pw"}}})
	if err != nil {
		t.Fatal(err)
	}
	before := repo.rows[v.RouteID].BasicAuthUsers
	if _, err := svc.UpdateRoute(context.Background(), v.RouteID, RouteRequest{Domain: "a.example.com", TargetHost: "h", TargetPort: 80,
		BasicAuth: []BasicAuthInput{{User: "admin"}}}); err != nil {
		t.Fatal(err)
	}
	if repo.rows[v.RouteID].BasicAuthUsers != before {
		t.Error("blank password should keep the stored hash")
	}
	if _, err := svc.UpdateRoute(context.Background(), v.RouteID, RouteRequest{Domain: "a.example.com", TargetHost: "h", TargetPort: 80}); err != nil {
		t.Fatal(err)
	}
	if repo.rows[v.RouteID].BasicAuthUsers != "" {
		t.Error("omitting basic_auth should clear it")
	}
}

func TestSetConfig_Validation(t *testing.T) {
	store := &fakeStore{}
	svc := NewService(log.New(nil), newFakeRepo(), store, nil, nil, nil, nil)
	ctx := context.Background()
	if _, err := svc.SetConfig(ctx, gateway.Config{Enabled: true}); !errors.Is(err, ErrValidation) {
		t.Error("enabled without node must fail")
	}
	if _, err := svc.SetConfig(ctx, gateway.Config{Enabled: true, Mode: gateway.ModeExternal, NodeMAC: "AA:BB"}); !errors.Is(err, ErrValidation) {
		t.Error("external without dir must fail")
	}
	if _, err := svc.SetConfig(ctx, gateway.Config{Enabled: true, NodeMAC: "AA:BB", ACMEEnabled: true}); !errors.Is(err, ErrValidation) {
		t.Error("acme without email must fail")
	}
	out, err := svc.SetConfig(ctx, gateway.Config{Enabled: true, NodeMAC: "AA:BB", ACMEEnabled: true, ACMEEmail: "ops@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if out.Mode != gateway.ModeManaged || out.HTTPPort != 80 || out.HTTPSPort != 443 || out.NodeMAC != "aa:bb" {
		t.Errorf("defaults: %+v", out)
	}
	cfg, err := LoadConfig(ctx, store)
	if err != nil || !cfg.Enabled || cfg.ACMEEmail != "ops@example.com" {
		t.Errorf("round-trip: %+v %v", cfg, err)
	}
}
