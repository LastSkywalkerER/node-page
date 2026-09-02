// Package controller implements the node-stats "controller" sidecar: a small
// privileged companion (the SAME image, run as `node-stats controller`) that
// owns the docker socket and applies the compose stack the main app asks for.
//
// The main app never touches the socket. It writes a desired-state descriptor
// to the shared data volume; the controller watches it, regenerates the base
// docker-compose.yml (via setup.BuildComposeContent), and runs `docker compose`
// against the host daemon — recreating ONLY the app service so the controller
// itself survives. This is how the setup wizard switches the database (sqlite ↔
// postgres), how the auto-updater pulls a new image and how the gateway's
// Traefik is started, without the app needing to restart the stack it lives in.
//
// Reconciliation is per UNIT (see units.go): compose file, db, app, traefik —
// each with its own applied hash, error and retry back-off, so one unit that
// cannot be applied never holds the others hostage.
package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/log"

	"system-stats/internal/platform/runtimetune"
	"system-stats/internal/platform/setup"
)

const pollInterval = 2 * time.Second

// runner executes docker / docker compose commands (swapped for a fake in tests).
type runner interface {
	compose(args ...string) (string, error)
	docker(args ...string) (string, error)
}

type controller struct {
	stackDir          string // compose project dir (identity-mounted host path)
	dataDir           string // shared data volume holding desired/status json
	project           string // compose -p project name
	appService        string
	controllerService string // compose service name of this controller sidecar
	run               runner
	now               func() time.Time

	applied     appliedUnits          // what has been applied (persisted)
	seeded      bool                  // applied has been initialised (file / legacy / baseline)
	units       map[string]*unitState // live per-unit state
	selfUpdated bool                  // guards the controller self-update to once per app apply
	lastStatus  string                // last status JSON written (skip identical rewrites)
}

// Run is the controller subcommand entrypoint (cmd/server: `node-stats controller`).
// It blocks forever, polling the desired-state descriptor and reconciling.
func Run() {
	c := newController(
		envOr("NODE_STATS_STACK_DIR", "/app/stack"),
		envOr("NODE_STATS_DATA_DIR", "/app/data"),
		envOr("NODE_STATS_PROJECT", "node-stats"),
		envOr("NODE_STATS_APP_SERVICE", "node-stats"),
		envOr("NODE_STATS_CONTROLLER_SERVICE", "node-stats-controller"),
	)
	c.run = &execRunner{stackDir: c.stackDir, project: c.project}
	log.Info("controller: starting", "stack_dir", c.stackDir, "data_dir", c.dataDir, "project", c.project)

	// Return freed heap to the OS on a slow cadence so the sidecar's RSS stays
	// near its (tiny) live heap instead of parking freed pages. Process-lifetime.
	runtimetune.StartPeriodicRelease(context.Background(), 2*time.Minute)

	if setup.ManagedExternally() {
		log.Warn("controller: deployment is managed externally (Dokploy/Traefik) — compose mutation disabled")
		c.writeStatus(setup.ControllerStatus{Phase: setup.PhaseDisabled, Message: "managed externally; node-stats will not mutate the compose stack"})
		c.idle()
		return
	}

	if err := c.checkCompose(); err != nil {
		log.Error("controller: docker compose unavailable", "error", err)
		c.writeStatus(setup.ControllerStatus{Phase: setup.PhaseError, Error: err.Error(),
			Message: "docker compose plugin missing in image — rebuild with docker-cli-compose"})
		c.idle()
		return
	}

	for {
		c.tick()
		time.Sleep(pollInterval)
	}
}

func newController(stackDir, dataDir, project, appService, controllerService string) *controller {
	c := &controller{
		stackDir: stackDir, dataDir: dataDir, project: project,
		appService: appService, controllerService: controllerService,
		now:   time.Now,
		units: map[string]*unitState{},
	}
	for _, u := range unitOrder {
		c.units[u] = &unitState{phase: setup.PhaseIdle}
	}
	return c
}

// seed initialises the applied record the first time a desired state is seen:
// from this controller's own file, else from the pre-unit controller's
// .applied-state.json (an upgrade must not recreate anything), else — when the
// installer's baseline compose already describes the desired topology — by
// treating the running stack as applied (the first desired-state ever written
// is typically a gateway-only change on top of a working install).
func (c *controller) seed(ds setup.DesiredState) {
	if c.seeded {
		return
	}
	c.seeded = true
	if a, ok := readAppliedUnits(c.dataDir); ok {
		c.applied = *a
		return
	}
	if prev := readLegacyAppliedState(c.dataDir); prev != nil {
		c.applied = unitsFor(*prev)
		log.Info("controller: migrated applied state from the previous controller", "generation", prev.Generation)
		writeAppliedUnits(c.dataDir, c.applied)
		return
	}
	base := ds
	base.Gateway, base.Generation, base.PullBeforeApply = nil, 0, false
	if onDisk, err := os.ReadFile(filepath.Join(c.stackDir, "docker-compose.yml")); err == nil && string(onDisk) == setup.BuildComposeContent(base) {
		c.applied = appliedUnits{DB: dbHash(base), App: appHash(base)}
		log.Info("controller: running stack matches the desired topology — app recreate skipped")
	}
}

// tick is one reconcile pass: read the desired state, then bring every unit
// whose desired hash differs from its applied one up to date, independently.
func (c *controller) tick() {
	ds, err := setup.ReadDesiredState(c.dataDir)
	if err != nil {
		log.Error("controller: read desired-state", "error", err)
		return
	}
	if ds == nil {
		return
	}
	c.seed(*ds)
	c.detectDrift(*ds)
	now := c.now()

	// compose file — everything else reads it.
	if !c.reconcile(unitCompose, composeHash(*ds), &c.applied.Compose, now, func() (string, error) { return c.applyCompose(*ds) }) {
		c.writeAggregate(*ds)
		return
	}
	c.reconcile(unitDB, dbHash(*ds), &c.applied.DB, now, func() (string, error) { return c.applyDB(*ds) })

	// app — after its database when that database is ours to run.
	pull := pullPending(*ds, c.applied)
	appWant := appHash(*ds)
	if ds.DBMode == setup.DBModePostgresManaged && c.applied.DB != dbHash(*ds) {
		if c.applied.App != appWant || pull {
			c.setUnit(unitApp, setup.PhaseIdle, "waiting for db", "", now)
		}
	} else if c.applied.App != appWant || pull {
		u := c.units[unitApp]
		if now.Before(u.nextRetry) {
			// still backing off
		} else {
			c.setUnit(unitApp, setup.PhaseApplying, "recreating "+c.appService, "", now)
			c.writeAggregate(*ds)
			msg, pulled, err := c.applyApp(*ds, pull)
			if err != nil {
				c.failUnit(unitApp, err, now)
			} else {
				c.applied.App = appWant
				if pulled {
					c.applied.AppPullGeneration = ds.Generation
				}
				c.okUnit(unitApp, msg, now)
				writeAppliedUnits(c.dataDir, c.applied)
				log.Info("controller: applied", "unit", unitApp, "generation", ds.Generation, "pulled", pulled)
				// The recreate may have moved the app to a new image; this
				// controller still runs the old one — follow it (see below).
				c.selfUpdated = false
				c.selfUpdateIfStale(*ds)
			}
		}
	}

	c.reconcile(unitTraefik, traefikHash(*ds), &c.applied.Traefik, now, func() (string, error) { return c.applyTraefik(*ds) })
	c.writeAggregate(*ds)
}

// reconcile applies one simple (hash-only) unit when it is out of date and not
// backing off. Returns whether the unit is applied afterwards.
func (c *controller) reconcile(name, want string, applied *string, now time.Time, apply func() (string, error)) bool {
	if *applied == want {
		return true
	}
	u := c.units[name]
	if now.Before(u.nextRetry) {
		return false
	}
	c.setUnit(name, setup.PhaseApplying, "applying "+name, "", now)
	msg, err := apply()
	if err != nil {
		c.failUnit(name, err, now)
		return false
	}
	*applied = want
	c.okUnit(name, msg, now)
	writeAppliedUnits(c.dataDir, c.applied)
	log.Info("controller: applied", "unit", name)
	return true
}

func (c *controller) setUnit(name, phase, msg, errStr string, now time.Time) {
	u := c.units[name]
	u.phase, u.message, u.err, u.updatedAt = phase, msg, errStr, now
}

func (c *controller) okUnit(name, msg string, now time.Time) {
	u := c.units[name]
	u.failures, u.nextRetry = 0, time.Time{}
	c.setUnit(name, setup.PhaseApplied, msg, "", now)
}

func (c *controller) failUnit(name string, err error, now time.Time) {
	u := c.units[name]
	u.failures++
	u.nextRetry = now.Add(backoff(u.failures))
	c.setUnit(name, setup.PhaseError, "apply failed", err.Error(), now)
	log.Error("controller: apply failed", "unit", name, "attempt", u.failures, "retry_in", backoff(u.failures).String(), "error", err)
}

// detectDrift notices an on-disk docker-compose.yml that no longer matches an
// ALREADY-APPLIED compose unit (an installer rerun or a manual edit clobbered
// it — classic: reverting a managed-postgres stack to the sqlite base and
// silently dropping the db service). The file is regenerated and the idempotent
// service units re-upped; the app is NOT recreated for a file drift.
func (c *controller) detectDrift(ds setup.DesiredState) {
	if c.applied.Compose == "" || c.applied.Compose != composeHash(ds) {
		return
	}
	got, err := os.ReadFile(filepath.Join(c.stackDir, "docker-compose.yml"))
	if err == nil && string(got) == setup.BuildComposeContent(ds) {
		return
	}
	log.Warn("controller: docker-compose.yml drifted from the applied desired state — restoring")
	c.applied.Compose, c.applied.DB, c.applied.Traefik = "", "", ""
}

// writeAggregate publishes controller-status.json: the per-unit view plus the
// legacy top-level summary (error if any unit errs, applying if any is in
// flight, else applied) that the wizard and update popup already understand.
func (c *controller) writeAggregate(ds setup.DesiredState) {
	st := setup.ControllerStatus{Generation: ds.Generation, Phase: setup.PhaseApplied, Services: map[string]setup.ServiceStatus{},
		PullAppliedGeneration: c.applied.AppPullGeneration}
	var msgs, errs []string
	for _, name := range unitOrder {
		u := c.units[name]
		ss := setup.ServiceStatus{Phase: u.phase, Message: u.message, Error: u.err}
		if !u.updatedAt.IsZero() {
			ss.UpdatedAt = u.updatedAt.UTC().Format(time.RFC3339)
		}
		if !u.nextRetry.IsZero() && u.phase == setup.PhaseError {
			ss.NextRetry = u.nextRetry.UTC().Format(time.RFC3339)
			ss.Attempts = u.failures
		}
		st.Services[name] = ss
		switch u.phase {
		case setup.PhaseError:
			errs = append(errs, name+": "+u.err)
		case setup.PhaseApplying:
			if st.Phase != setup.PhaseError {
				st.Phase = setup.PhaseApplying
			}
			msgs = append(msgs, u.message)
		case setup.PhaseApplied:
			if u.message != "" && name == unitApp {
				msgs = append(msgs, u.message)
			}
		}
	}
	if len(errs) > 0 {
		st.Phase = setup.PhaseError
		st.Error = strings.Join(errs, " | ")
		st.Message = "some services failed to apply (retrying)"
	} else if len(msgs) > 0 {
		st.Message = strings.Join(msgs, "; ")
	}
	c.writeStatus(st)
}

// selfUpdateIfStale recreates this controller when the app service now runs a
// different image than the controller's current process. Runs at most once per
// app apply (guarded by c.selfUpdated). Best-effort: any inspection error is
// logged and skipped rather than risking a recreate loop.
func (c *controller) selfUpdateIfStale(ds setup.DesiredState) {
	if c.selfUpdated {
		return
	}
	appImg, err := c.serviceImageID(c.appService)
	if err != nil || appImg == "" {
		log.Debug("controller: self-update skipped — app image id unavailable", "error", err)
		return
	}
	selfImg, err := c.serviceImageID(c.controllerService)
	if err != nil || selfImg == "" {
		log.Debug("controller: self-update skipped — controller image id unavailable", "error", err)
		return
	}
	if appImg == selfImg {
		return
	}
	c.selfUpdated = true
	log.Info("controller: self-update — recreating controller onto the app's new image",
		"app_image", appImg, "controller_image", selfImg)
	c.setUnit(unitApp, setup.PhaseApplied, "recreating controller onto the new image", "", c.now())
	c.writeAggregate(ds)

	// A container cannot `docker compose up` ITSELF: the moment compose stops the
	// old controller to recreate it, the command running INSIDE that container is
	// killed before it can start the replacement — leaving NO controller (and a
	// half-created stray). Instead spawn a DETACHED sibling container (on the new
	// image, with the docker socket + the identity-mounted stack dir) that waits a
	// beat and then recreates the controller from OUTSIDE. It survives this
	// controller's death and self-removes when done. Best-effort: if the spawn
	// fails, this controller simply keeps running (no self-update) rather than
	// killing itself. The persisted applied-units file stops the new controller
	// from re-applying anything.
	helperName := c.controllerService + "-selfupdate"
	_, _ = c.run.docker("rm", "-f", helperName) // clear a prior helper if any
	recreate := fmt.Sprintf(
		"sleep 3; docker compose -p %s --project-directory %s up -d --no-deps --force-recreate %s",
		c.project, c.stackDir, c.controllerService)
	helper := []string{
		"run", "-d", "--rm", "--name", helperName,
		"-v", "/var/run/docker.sock:/var/run/docker.sock",
		"-v", c.stackDir + ":" + c.stackDir,
		"-w", c.stackDir,
		"--entrypoint", "sh",
		appImg, "-c", recreate,
	}
	if out, err := c.run.docker(helper...); err != nil {
		log.Error("controller: self-update helper spawn failed", "out", out, "error", err)
	}
}

// serviceImageID resolves the docker image ID a running compose service uses, by
// asking compose for the container id and inspecting it. Returns "" with no error
// when the service has no running container.
func (c *controller) serviceImageID(service string) (string, error) {
	cid, err := c.run.compose("ps", "-q", service)
	if err != nil {
		return "", err
	}
	cid = firstLine(cid)
	if cid == "" {
		return "", nil
	}
	out, err := c.run.docker("inspect", "--format", "{{.Image}}", cid)
	if err != nil {
		return "", err
	}
	return firstLine(out), nil
}

// readLegacyAppliedState loads the pre-unit controller's .applied-state.json
// (the full last-applied descriptor) for a one-time migration.
func readLegacyAppliedState(dir string) *setup.DesiredState {
	b, err := os.ReadFile(filepath.Join(dir, ".applied-state.json"))
	if err != nil {
		return nil
	}
	var ds setup.DesiredState
	if err := json.Unmarshal(b, &ds); err != nil {
		return nil
	}
	return &ds
}

// execRunner runs the real docker CLI against the host daemon.
type execRunner struct {
	stackDir, project string
}

// compose runs `docker compose -p <project> --project-directory <stackDir> ...`
// and returns combined output (for status messages) plus any error.
func (r *execRunner) compose(args ...string) (string, error) {
	full := append([]string{"compose", "-p", r.project, "--project-directory", r.stackDir}, args...)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", full...)
	cmd.Dir = r.stackDir
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// docker runs a plain `docker ...` command (not `docker compose`) — image
// prune, inspect, the self-update helper.
func (r *execRunner) docker(args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Dir = r.stackDir
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// checkCompose verifies the docker compose plugin is present in the image.
func (c *controller) checkCompose() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, "docker", "compose", "version").Run()
}

func (c *controller) writeStatus(st setup.ControllerStatus) {
	key, _ := json.Marshal(st)
	if string(key) == c.lastStatus {
		return
	}
	c.lastStatus = string(key)
	st.UpdatedAt = c.now().UTC().Format(time.RFC3339)
	if err := setup.WriteControllerStatus(c.dataDir, st); err != nil {
		log.Error("controller: write status", "error", err)
	}
}

// idle parks the controller (managed-externally / no-compose) so the container
// stays up and keeps reporting its disabled status without touching anything.
func (c *controller) idle() {
	for {
		time.Sleep(time.Hour)
	}
}

// tailLines keeps the last n non-empty lines of a (possibly huge, pull-
// progress-laden) compose output for a readable status message.
func tailLines(s string, n int) string {
	var lines []string
	for _, l := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(l); t != "" {
			lines = append(lines, t)
		}
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, " | ")
}

// firstLine returns the first non-empty line of s, trimmed. `compose ps -q` can
// emit several ids (or a trailing newline); we only want one container id.
func firstLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(line); t != "" {
			return t
		}
	}
	return ""
}

func envOr(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

// upsertEnvKey sets KEY=value in a dotenv-style file, replacing an existing
// (uncommented) line for KEY or appending one, and preserving every other line
// (comments included). Creates the file if missing. The stack .env is the
// installer's, so we touch only the single key requested.
func upsertEnvKey(path, key, value string) error {
	var lines []string
	if b, err := os.ReadFile(path); err == nil {
		lines = strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	} else if !os.IsNotExist(err) {
		return err
	}
	prefix := key + "="
	replaced := false
	for i, ln := range lines {
		trimmed := strings.TrimSpace(ln)
		if strings.HasPrefix(trimmed, prefix) {
			lines[i] = key + "=" + value
			replaced = true
			break
		}
	}
	if !replaced {
		lines = append(lines, key+"="+value)
	}
	out := strings.Join(lines, "\n") + "\n"
	return writeFileAtomic(path, []byte(out))
}

// writeFileAtomic writes data via a temp file + rename so a reader (or compose)
// never sees a half-written docker-compose.yml.
func writeFileAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
