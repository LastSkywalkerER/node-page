// Package controller implements the node-stats "controller" sidecar: a small
// privileged companion (the SAME image, run as `node-stats controller`) that
// owns the docker socket and applies the compose stack the main app asks for.
//
// The main app never touches the socket. It writes a desired-state descriptor
// to the shared data volume; the controller watches it, regenerates the base
// docker-compose.yml (via setup.BuildComposeContent), and runs `docker compose`
// against the host daemon — recreating ONLY the app service so the controller
// itself survives. This is how the setup wizard switches the database (sqlite ↔
// postgres) and how the auto-updater pulls a new image, without the app needing
// to restart the stack it lives in.
package controller

import (
	"context"
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

type controller struct {
	stackDir          string // compose project dir (identity-mounted host path)
	dataDir           string // shared data volume holding desired/status json
	project           string // compose -p project name
	appService        string
	controllerService string // compose service name of this controller sidecar
	lastApplied       string // hash of the last successfully applied desired state
	selfUpdated       bool   // guards the controller self-update to once per apply
}

// Run is the controller subcommand entrypoint (cmd/server: `node-stats controller`).
// It blocks forever, polling the desired-state descriptor and applying changes.
func Run() {
	c := &controller{
		stackDir:          envOr("NODE_STATS_STACK_DIR", "/app/stack"),
		dataDir:           envOr("NODE_STATS_DATA_DIR", "/app/data"),
		project:           envOr("NODE_STATS_PROJECT", "node-stats"),
		appService:        envOr("NODE_STATS_APP_SERVICE", "node-stats"),
		controllerService: envOr("NODE_STATS_CONTROLLER_SERVICE", "node-stats-controller"),
	}
	c.lastApplied = readAppliedHash(c.dataDir)
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

	c.loop()
}

// loop polls the descriptor and applies it when its hash changes.
func (c *controller) loop() {
	for {
		ds, err := setup.ReadDesiredState(c.dataDir)
		if err != nil {
			log.Error("controller: read desired-state", "error", err)
		} else if ds != nil {
			h := ds.Hash()
			switch {
			case h != c.lastApplied:
				if c.apply(*ds) {
					c.lastApplied = h
				}
			case c.composeDrifted(*ds):
				// The file no longer matches an ALREADY-APPLIED state - an
				// installer rerun or a manual edit clobbered it (classic:
				// reverting a managed-postgres stack to the sqlite base and
				// silently dropping the db service). Restore reality.
				log.Warn("controller: docker-compose.yml drifted from the applied desired state - restoring")
				c.apply(*ds)
			}
		}
		time.Sleep(pollInterval)
	}
}

// apply regenerates the compose file and recreates the app service. Returns
// true when the apply succeeded (so loop records the hash and stops retrying).
func (c *controller) apply(ds setup.DesiredState) bool {
	log.Info("controller: applying desired state", "generation", ds.Generation, "db_mode", ds.DBMode)
	c.writeStatus(setup.ControllerStatus{Generation: ds.Generation, Phase: setup.PhaseApplying,
		Message: "regenerating compose and recreating " + c.appService})
	c.selfUpdated = false // fresh apply — allow at most one self-update below

	compose := setup.BuildComposeContent(ds)
	if err := writeFileAtomic(filepath.Join(c.stackDir, "docker-compose.yml"), []byte(compose)); err != nil {
		return c.fail(ds, "write compose", err)
	}

	// A port change from the in-app settings arrives as HTTPPort/RaftPort on the
	// desired state. Compose reads the published ports from the stack .env
	// (NODE_STATS_PORT / NODE_STATS_RAFT_PORT), which only the controller can
	// write — upsert them here before the recreate so the new mapping applies.
	if strings.TrimSpace(ds.HTTPPort) != "" || strings.TrimSpace(ds.RaftPort) != "" {
		envPath := filepath.Join(c.stackDir, ".env")
		if ds.HTTPPort != "" {
			if err := upsertEnvKey(envPath, "NODE_STATS_PORT", ds.HTTPPort); err != nil {
				return c.fail(ds, "update stack .env (NODE_STATS_PORT)", err)
			}
		}
		if ds.RaftPort != "" {
			if err := upsertEnvKey(envPath, "NODE_STATS_RAFT_PORT", ds.RaftPort); err != nil {
				return c.fail(ds, "update stack .env (NODE_STATS_RAFT_PORT)", err)
			}
		}
	}

	managed := ds.DBMode == setup.DBModePostgresManaged
	if managed {
		// Bring the DB up and wait for its healthcheck before recreating the app
		// (which depends_on db: service_healthy). --wait blocks until healthy.
		if out, err := c.compose("up", "-d", "--wait", "db"); err != nil {
			return c.fail(ds, "start db: "+out, err)
		}
	} else {
		// Switching off managed Postgres: tear down the orphaned db so its
		// container/healthcheck don't linger (--remove-orphans below also catches
		// it, but an explicit stop+rm is clearer and survives `db` still being
		// referenced by a stale override). Best-effort — ignore no-such-service.
		if out, err := c.compose("stop", "db"); err != nil {
			log.Debug("controller: stop db (ignored)", "out", out, "error", err)
		}
		if out, err := c.compose("rm", "-f", "db"); err != nil {
			log.Debug("controller: rm db (ignored)", "out", out, "error", err)
		}
	}

	if ds.PullBeforeApply {
		if out, err := c.compose("pull", c.appService); err != nil {
			return c.fail(ds, "pull "+c.appService+": "+out, err)
		}
	}

	// Recreate ONLY the app: --no-deps keeps the controller (and db) untouched;
	// --force-recreate restarts the app even when its compose stanza is unchanged
	// so it re-reads the bind-mounted .env.agent (DB_DSN lives there, invisible
	// to compose's change detection). --remove-orphans drops services no longer
	// in the generated compose (e.g. the db after a switch away from managed PG).
	if out, err := c.compose("up", "-d", "--no-deps", "--force-recreate", "--remove-orphans", c.appService); err != nil {
		return c.fail(ds, "recreate "+c.appService+": "+out, err)
	}

	log.Info("controller: applied", "generation", ds.Generation)
	writeAppliedHash(c.dataDir, ds.Hash())
	c.writeStatus(setup.ControllerStatus{Generation: ds.Generation, Phase: setup.PhaseApplied,
		Message: "recreated " + c.appService})

	if ds.PullBeforeApply {
		// Reclaim the now-dangling old image layers the pull replaced. Dangling-only
		// (no `-a`), so it never removes images still referenced by a service.
		if out, err := c.dockerCLI("image", "prune", "-f"); err != nil {
			log.Debug("controller: image prune (ignored)", "out", out, "error", err)
		} else {
			log.Info("controller: pruned dangling images")
		}
	}

	// Controller self-update: the recreate above may have moved the app to a new
	// image; this controller is still running the previous one. Recreate ourselves
	// so the whole stack ends up on the same image. The docker daemon completes the
	// recreate after this process dies mid-command; the persisted .applied-hash
	// above stops the new controller from re-applying and looping.
	c.selfUpdateIfStale(ds)
	return true
}

// selfUpdateIfStale recreates this controller when the app service now runs a
// different image than the controller's current process. Runs at most once per
// apply (guarded by c.selfUpdated). Best-effort: any inspection error is logged
// and skipped rather than risking a recreate loop.
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
	c.writeStatus(setup.ControllerStatus{Generation: ds.Generation, Phase: setup.PhaseApplied,
		Message: "recreating controller onto the new image"})

	// A container cannot `docker compose up` ITSELF: the moment compose stops the
	// old controller to recreate it, the command running INSIDE that container is
	// killed before it can start the replacement — leaving NO controller (and a
	// half-created stray). Instead spawn a DETACHED sibling container (on the new
	// image, with the docker socket + the identity-mounted stack dir) that waits a
	// beat and then recreates the controller from OUTSIDE. It survives this
	// controller's death and self-removes when done. Best-effort: if the spawn
	// fails, this controller simply keeps running (no self-update) rather than
	// killing itself.
	helperName := c.controllerService + "-selfupdate"
	_, _ = c.dockerCLI("rm", "-f", helperName) // clear a prior helper if any
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
	if out, err := c.dockerCLI(helper...); err != nil {
		log.Error("controller: self-update helper spawn failed", "out", out, "error", err)
	}
}

// serviceImageID resolves the docker image ID a running compose service uses, by
// asking compose for the container id and inspecting it. Returns "" with no error
// when the service has no running container.
func (c *controller) serviceImageID(service string) (string, error) {
	cid, err := c.compose("ps", "-q", service)
	if err != nil {
		return "", err
	}
	cid = firstLine(cid)
	if cid == "" {
		return "", nil
	}
	out, err := c.dockerCLI("inspect", "--format", "{{.Image}}", cid)
	if err != nil {
		return "", err
	}
	return firstLine(out), nil
}

// composeDrifted reports whether the on-disk docker-compose.yml differs from
// what the given desired state generates. Only consulted for a desired state
// the controller has already applied, so re-applying restores a previously
// working topology and never springs a half-finished DB switch on the stack.
func (c *controller) composeDrifted(ds setup.DesiredState) bool {
	want := setup.BuildComposeContent(ds)
	got, err := os.ReadFile(filepath.Join(c.stackDir, "docker-compose.yml"))
	if err != nil {
		return true
	}
	return string(got) != want
}

// applied-hash persistence so a controller restart doesn't re-apply an already
// applied desired state (which would needlessly recreate the app).
func readAppliedHash(dir string) string {
	b, err := os.ReadFile(filepath.Join(dir, ".applied-hash"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func writeAppliedHash(dir, hash string) {
	tmp := filepath.Join(dir, ".applied-hash.tmp")
	if err := os.WriteFile(tmp, []byte(hash), 0o644); err == nil {
		_ = os.Rename(tmp, filepath.Join(dir, ".applied-hash"))
	}
}

func (c *controller) fail(ds setup.DesiredState, where string, err error) bool {
	log.Error("controller: apply failed", "where", where, "error", err)
	c.writeStatus(setup.ControllerStatus{Generation: ds.Generation, Phase: setup.PhaseError,
		Message: where, Error: err.Error()})
	return false // do not record the hash → retry on the next poll
}

// compose runs `docker compose -p <project> --project-directory <stackDir> ...`
// and returns combined output (for status messages) plus any error.
func (c *controller) compose(args ...string) (string, error) {
	full := append([]string{"compose", "-p", c.project, "--project-directory", c.stackDir}, args...)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", full...)
	cmd.Dir = c.stackDir
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// dockerCLI runs a plain `docker ...` command (not `docker compose`) and returns
// combined output plus any error — used for image prune and inspect.
func (c *controller) dockerCLI(args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Dir = c.stackDir
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
	st.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
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
