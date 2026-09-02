package controller

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"system-stats/internal/platform/setup"
)

// fakeRunner records compose/docker calls and fails the ones a test asks for.
type fakeRunner struct {
	calls   []string
	failing map[string]error // prefix of the joined compose args → error
}

func (f *fakeRunner) compose(args ...string) (string, error) {
	line := strings.Join(args, " ")
	f.calls = append(f.calls, "compose "+line)
	for prefix, err := range f.failing {
		if strings.HasPrefix(line, prefix) {
			return "boom", err
		}
	}
	return "", nil
}

func (f *fakeRunner) docker(args ...string) (string, error) {
	f.calls = append(f.calls, "docker "+strings.Join(args, " "))
	return "", nil
}

func (f *fakeRunner) count(prefix string) int {
	n := 0
	for _, c := range f.calls {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return n
}

func newTestController(t *testing.T) (*controller, *fakeRunner, *time.Time) {
	t.Helper()
	stack, data := t.TempDir(), t.TempDir()
	c := newController(stack, data, "ns", "node-stats", "node-stats-controller")
	fr := &fakeRunner{failing: map[string]error{}}
	c.run = fr
	now := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	c.now = func() time.Time { return now }
	return c, fr, &now
}

func writeDS(t *testing.T, c *controller, ds setup.DesiredState) {
	t.Helper()
	if err := setup.WriteDesiredState(c.dataDir, ds); err != nil {
		t.Fatal(err)
	}
}

func readStatus(t *testing.T, c *controller) setup.ControllerStatus {
	t.Helper()
	st, err := setup.ReadControllerStatus(c.dataDir)
	if err != nil || st == nil {
		t.Fatalf("status: %v %v", st, err)
	}
	return *st
}

const appUp = "compose up -d --no-deps --force-recreate --remove-orphans node-stats"

func TestTick_TraefikFailureDoesNotBlockAppUpdate(t *testing.T) {
	c, fr, now := newTestController(t)
	fr.failing["up -d --wait traefik"] = errors.New("port 80 taken")
	ds := setup.DesiredState{Generation: 5, DBMode: setup.DBModeSQLite, Image: "img:1", PullBeforeApply: true,
		Gateway: &setup.GatewayProvision{Enabled: true}}
	writeDS(t, c, ds)

	c.tick()
	if fr.count("compose pull node-stats") != 1 || fr.count(appUp) != 1 {
		t.Fatalf("app must be pulled+recreated despite traefik failing: %v", fr.calls)
	}
	if fr.count("compose up -d --wait traefik") != 1 {
		t.Fatalf("traefik attempted once: %v", fr.calls)
	}
	st := readStatus(t, c)
	if st.Phase != setup.PhaseError || st.Services["app"].Phase != setup.PhaseApplied || st.Services["traefik"].Phase != setup.PhaseError || st.PullAppliedGeneration != 5 {
		t.Fatalf("status: %+v", st)
	}
	if !strings.Contains(st.UnitView(setup.ServiceUnitTraefik).Error, "port 80 taken") || st.UnitView(setup.ServiceUnitTraefik).Phase != setup.PhaseError {
		t.Errorf("traefik unit view: %+v", st.UnitView(setup.ServiceUnitTraefik))
	}
	if b, _ := os.ReadFile(filepath.Join(c.stackDir, "docker-compose.yml")); !strings.Contains(string(b), "  traefik:") {
		t.Error("compose must contain the traefik stanza")
	}

	// Back-off: the very next tick does not retry; after 2s it does — and the
	// app is NOT recreated again.
	c.tick()
	if fr.count("compose up -d --wait traefik") != 1 {
		t.Fatalf("retry before back-off elapsed: %v", fr.calls)
	}
	*now = now.Add(3 * time.Second)
	c.tick()
	if fr.count("compose up -d --wait traefik") != 2 || fr.count(appUp) != 1 || fr.count("compose pull node-stats") != 1 {
		t.Fatalf("expected a traefik retry only: %v", fr.calls)
	}
	// Failures back off exponentially (2s, 4s, …).
	*now = now.Add(3 * time.Second)
	c.tick()
	if fr.count("compose up -d --wait traefik") != 2 {
		t.Fatalf("second retry must wait 4s: %v", fr.calls)
	}

	// The clashing proxy goes away → the next due retry succeeds and the unit
	// is recorded; nothing else is touched.
	delete(fr.failing, "up -d --wait traefik")
	*now = now.Add(5 * time.Second)
	c.tick()
	st = readStatus(t, c)
	if st.Phase != setup.PhaseApplied || st.Services["traefik"].Phase != setup.PhaseApplied || fr.count(appUp) != 1 {
		t.Fatalf("after recovery: %+v calls=%v", st, fr.calls)
	}
	a, ok := readAppliedUnits(c.dataDir)
	if !ok || a.Traefik != traefikHash(ds) || a.App != appHash(ds) || a.AppPullGeneration != 5 {
		t.Fatalf("applied units: %+v", a)
	}
}

func TestTick_GatewayOnlyChangeNeverRecreatesApp_PullIntentSurvivesFlagClear(t *testing.T) {
	c, fr, _ := newTestController(t)
	ds := setup.DesiredState{Generation: 1, DBMode: setup.DBModeSQLite, Image: "img:1", PullBeforeApply: true}
	writeDS(t, c, ds)
	c.tick()
	if fr.count(appUp) != 1 || fr.count("compose pull node-stats") != 1 {
		t.Fatalf("initial apply: %v", fr.calls)
	}

	// Gateway toggled on (pull flag cleared by the writer since it was applied).
	ds.Generation, ds.PullBeforeApply = 2, false
	ds.Gateway = &setup.GatewayProvision{Enabled: true}
	writeDS(t, c, ds)
	c.tick()
	if fr.count(appUp) != 1 || fr.count("compose up -d --wait traefik") != 1 {
		t.Fatalf("gateway-only change must only touch traefik: %v", fr.calls)
	}
	// Gateway ports changed → traefik re-upped, app untouched.
	ds.Generation, ds.Gateway = 3, &setup.GatewayProvision{Enabled: true, HTTPPort: 8080}
	writeDS(t, c, ds)
	c.tick()
	if fr.count(appUp) != 1 || fr.count("compose up -d --wait traefik") != 2 {
		t.Fatalf("port change: %v", fr.calls)
	}
	// Gateway off → traefik stopped + removed; app untouched.
	ds.Generation, ds.Gateway = 4, &setup.GatewayProvision{Enabled: false}
	writeDS(t, c, ds)
	c.tick()
	// (the first tick already ran one best-effort stop/rm for the "absent" state)
	if fr.count("compose stop traefik") != 2 || fr.count("compose rm -f traefik") != 2 || fr.count(appUp) != 1 {
		t.Fatalf("gateway off: %v", fr.calls)
	}
	// A new update request (pull, new generation) → pull + recreate once.
	ds.Generation, ds.PullBeforeApply = 5, true
	writeDS(t, c, ds)
	c.tick()
	c.tick()
	if fr.count("compose pull node-stats") != 2 || fr.count(appUp) != 2 {
		t.Fatalf("update request: %v", fr.calls)
	}
	// Image (channel) change without pull flag → recreate, no pull.
	ds.Generation, ds.PullBeforeApply, ds.Image = 6, false, "img:beta"
	writeDS(t, c, ds)
	c.tick()
	if fr.count("compose pull node-stats") != 2 || fr.count(appUp) != 3 {
		t.Fatalf("image change: %v", fr.calls)
	}
}

func TestTick_PullFailureKeepsRetrying_TraefikStillApplied(t *testing.T) {
	c, fr, now := newTestController(t)
	fr.failing["pull node-stats"] = errors.New("registry down")
	ds := setup.DesiredState{Generation: 9, DBMode: setup.DBModeSQLite, Image: "img:2", PullBeforeApply: true,
		Gateway: &setup.GatewayProvision{Enabled: true}}
	writeDS(t, c, ds)
	c.tick()
	st := readStatus(t, c)
	if fr.count(appUp) != 0 || fr.count("compose up -d --wait traefik") != 1 || st.Services["app"].Phase != setup.PhaseError || st.Services["traefik"].Phase != setup.PhaseApplied || st.PullAppliedGeneration != 0 {
		t.Fatalf("pull failure: %+v %v", st, fr.calls)
	}
	// The pull is still pending: a gateway-only writer must NOT drop the flag
	// (RequestGatewayState consults PullAppliedGeneration).
	changed, err := setup.ReconcileGatewayDesiredState(c.dataDir, "sqlite", "", setup.GatewayProvision{Enabled: true, HTTPPort: 8080})
	if err != nil || !changed {
		t.Fatalf("request gateway state: %v %v", changed, err)
	}
	next, _ := setup.ReadDesiredState(c.dataDir)
	if !next.PullBeforeApply || next.Generation != 10 {
		t.Fatalf("pending pull dropped: %+v", next)
	}
	delete(fr.failing, "pull node-stats")
	*now = now.Add(5 * time.Second)
	c.tick()
	st = readStatus(t, c)
	if fr.count("compose pull node-stats") != 2 || fr.count(appUp) != 1 || st.PullAppliedGeneration != 10 || st.Phase != setup.PhaseApplied {
		t.Fatalf("recovery: %+v %v", st, fr.calls)
	}
	// Once applied, the writer clears the flag on the next gateway-only change.
	if _, err := setup.ReconcileGatewayDesiredState(c.dataDir, "sqlite", "", setup.GatewayProvision{Enabled: false}); err != nil {
		t.Fatal(err)
	}
	next, _ = setup.ReadDesiredState(c.dataDir)
	if next.PullBeforeApply {
		t.Fatalf("applied pull must be cleared: %+v", next)
	}
}

func TestTick_ManagedDBGatesAppButNotTraefik(t *testing.T) {
	c, fr, now := newTestController(t)
	fr.failing["up -d --wait db"] = errors.New("db unhealthy")
	ds := setup.DesiredState{Generation: 1, DBMode: setup.DBModePostgresManaged, DBDSN: "host=db", Image: "img:1",
		DB: setup.DBProvision{Name: "n", User: "u", Password: "p"}, Gateway: &setup.GatewayProvision{Enabled: true}}
	writeDS(t, c, ds)
	c.tick()
	st := readStatus(t, c)
	if fr.count(appUp) != 0 || fr.count("compose up -d --wait traefik") != 1 || st.Services["db"].Phase != setup.PhaseError || st.Services["app"].Message != "waiting for db" {
		t.Fatalf("db failure: %+v %v", st, fr.calls)
	}
	delete(fr.failing, "up -d --wait db")
	*now = now.Add(3 * time.Second)
	c.tick()
	if fr.count(appUp) != 1 || readStatus(t, c).Phase != setup.PhaseApplied {
		t.Fatalf("db recovery: %v", fr.calls)
	}
}

func TestSeed_LegacyAppliedStateAndBaselineCompose(t *testing.T) {
	// Legacy controller left .applied-state.json → nothing is re-applied.
	c, fr, _ := newTestController(t)
	prev := setup.DesiredState{Generation: 3, DBMode: setup.DBModeSQLite, Image: "img:1", PullBeforeApply: true}
	b, _ := setup.ReadDesiredState(c.dataDir)
	_ = b
	_ = writeFileAtomic(filepath.Join(c.dataDir, ".applied-state.json"), []byte(`{"generation":3,"db_mode":"sqlite","image":"img:1","pull_before_apply":true}`))
	_ = writeFileAtomic(filepath.Join(c.stackDir, "docker-compose.yml"), []byte(setup.BuildComposeContent(prev)))
	writeDS(t, c, prev)
	c.tick()
	if len(fr.calls) != 0 {
		t.Fatalf("legacy applied state must seed everything: %v", fr.calls)
	}
	// Then a gateway-only change → only traefik.
	next := prev
	next.Generation, next.PullBeforeApply, next.Gateway = 4, false, &setup.GatewayProvision{Enabled: true}
	writeDS(t, c, next)
	c.tick()
	if fr.count(appUp) != 0 || fr.count("compose up -d --wait traefik") != 1 {
		t.Fatalf("after legacy seed: %v", fr.calls)
	}

	// Fresh install: the installer's baseline compose already matches the
	// desired sqlite topology → the very first (gateway-only) desired state
	// does not recreate the app.
	c2, fr2, _ := newTestController(t)
	base := setup.DesiredState{DBMode: setup.DBModeSQLite, Image: "img:1"}
	_ = writeFileAtomic(filepath.Join(c2.stackDir, "docker-compose.yml"), []byte(setup.BuildComposeContent(base)))
	first := base
	first.Generation, first.Gateway = 1, &setup.GatewayProvision{Enabled: true}
	writeDS(t, c2, first)
	c2.tick()
	if fr2.count(appUp) != 0 || fr2.count("compose up -d --wait traefik") != 1 {
		t.Fatalf("baseline seed: %v", fr2.calls)
	}
	// …but a DB switch on top of the baseline does recreate it.
	c3, fr3, _ := newTestController(t)
	_ = writeFileAtomic(filepath.Join(c3.stackDir, "docker-compose.yml"), []byte(setup.BuildComposeContent(base)))
	pg := setup.DesiredState{Generation: 1, DBMode: setup.DBModePostgresExternal, DBDSN: "host=x", Image: "img:1"}
	writeDS(t, c3, pg)
	c3.tick()
	if fr3.count(appUp) != 1 {
		t.Fatalf("db switch must recreate: %v", fr3.calls)
	}
}

func TestDetectDrift_RewritesComposeWithoutRecreatingApp(t *testing.T) {
	c, fr, _ := newTestController(t)
	ds := setup.DesiredState{Generation: 1, DBMode: setup.DBModeSQLite, Image: "img:1", Gateway: &setup.GatewayProvision{Enabled: true}}
	writeDS(t, c, ds)
	c.tick()
	if fr.count(appUp) != 1 {
		t.Fatalf("initial: %v", fr.calls)
	}
	_ = writeFileAtomic(filepath.Join(c.stackDir, "docker-compose.yml"), []byte("services: {}\n"))
	c.tick()
	b, _ := os.ReadFile(filepath.Join(c.stackDir, "docker-compose.yml"))
	if string(b) != setup.BuildComposeContent(ds) {
		t.Fatal("compose not restored")
	}
	if fr.count(appUp) != 1 || fr.count("compose up -d --wait traefik") != 2 {
		t.Fatalf("drift must re-up traefik but not recreate the app: %v", fr.calls)
	}
}

func TestBackoff(t *testing.T) {
	for i, want := range []time.Duration{0, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 32 * time.Second, time.Minute, time.Minute} {
		if got := backoff(i); got != want {
			t.Errorf("backoff(%d) = %v, want %v", i, got, want)
		}
	}
}
