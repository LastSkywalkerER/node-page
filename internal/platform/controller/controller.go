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
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/log"

	"system-stats/internal/platform/setup"
)

const pollInterval = 2 * time.Second

type controller struct {
	stackDir    string // compose project dir (identity-mounted host path)
	dataDir     string // shared data volume holding desired/status json
	project     string // compose -p project name
	appService  string
	lastApplied string // hash of the last successfully applied desired state
}

// Run is the controller subcommand entrypoint (cmd/server: `node-stats controller`).
// It blocks forever, polling the desired-state descriptor and applying changes.
func Run() {
	c := &controller{
		stackDir:   envOr("NODE_STATS_STACK_DIR", "/app/stack"),
		dataDir:    envOr("NODE_STATS_DATA_DIR", "/app/data"),
		project:    envOr("NODE_STATS_PROJECT", "node-stats"),
		appService: envOr("NODE_STATS_APP_SERVICE", "node-stats"),
	}
	c.lastApplied = readAppliedHash(c.dataDir)
	log.Info("controller: starting", "stack_dir", c.stackDir, "data_dir", c.dataDir, "project", c.project)

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
			if h := ds.Hash(); h != c.lastApplied {
				if c.apply(*ds) {
					c.lastApplied = h
				}
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

	compose := setup.BuildComposeContent(ds)
	if err := writeFileAtomic(filepath.Join(c.stackDir, "docker-compose.yml"), []byte(compose)); err != nil {
		return c.fail(ds, "write compose", err)
	}

	managed := ds.DBMode == setup.DBModePostgresManaged
	if managed {
		// Bring the DB up and wait for its healthcheck before recreating the app
		// (which depends_on db: service_healthy). --wait blocks until healthy.
		if out, err := c.compose("up", "-d", "--wait", "db"); err != nil {
			return c.fail(ds, "start db: "+out, err)
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
	// to compose's change detection).
	if out, err := c.compose("up", "-d", "--no-deps", "--force-recreate", c.appService); err != nil {
		return c.fail(ds, "recreate "+c.appService+": "+out, err)
	}

	log.Info("controller: applied", "generation", ds.Generation)
	writeAppliedHash(c.dataDir, ds.Hash())
	c.writeStatus(setup.ControllerStatus{Generation: ds.Generation, Phase: setup.PhaseApplied,
		Message: "recreated " + c.appService})
	return true
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

func envOr(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
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
