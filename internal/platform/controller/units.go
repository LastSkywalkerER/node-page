package controller

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/log"

	"system-stats/internal/platform/setup"
)

// The stack is reconciled as INDEPENDENT units, each with its own desired
// hash, applied hash, error and retry back-off — so a unit that cannot be
// applied right now (the gateway's :80 taken by another proxy, a registry
// that is down for the pull, a Postgres that will not become healthy) never
// blocks the others: the app still updates while Traefik retries, Traefik
// still comes up while a pull fails, and a unit that eventually succeeds is
// recorded on its own.
//
//	compose  — the generated docker-compose.yml + the stack .env keys it reads
//	db       — the managed Postgres service (present / absent)
//	app      — the node-stats service (image, DB env, ports; plus one-shot pulls)
//	traefik  — the managed gateway service (present with its provision / absent)
//
// Order within a tick is compose → db → app → traefik (the file first, the app
// after its database), but a failure only skips what really depends on it.
const (
	unitCompose = "compose"
	unitDB      = "db"
	unitApp     = "app"
	unitTraefik = "traefik"
)

var unitOrder = []string{unitCompose, unitDB, unitApp, unitTraefik}

// appliedUnits is what the controller has successfully applied, persisted to
// <dataDir>/.applied-units.json so a controller restart (or the self-update
// recreate) never re-applies — and never re-recreates the app — for nothing.
type appliedUnits struct {
	Compose string `json:"compose,omitempty"`
	DB      string `json:"db,omitempty"`
	App     string `json:"app,omitempty"`
	// AppPullGeneration is the desired-state generation whose pull request the
	// app unit last executed: a pull is a one-shot INTENT keyed by generation,
	// not part of the app's topology hash, so clearing the flag later (a
	// gateway-only change) never recreates the app again.
	AppPullGeneration int    `json:"app_pull_generation,omitempty"`
	Traefik           string `json:"traefik,omitempty"`
}

const appliedUnitsFile = ".applied-units.json"

func readAppliedUnits(dir string) (*appliedUnits, bool) {
	b, err := os.ReadFile(filepath.Join(dir, appliedUnitsFile))
	if err != nil {
		return nil, false
	}
	var a appliedUnits
	if err := json.Unmarshal(b, &a); err != nil {
		return nil, false
	}
	return &a, true
}

func writeAppliedUnits(dir string, a appliedUnits) {
	b, err := json.Marshal(a)
	if err != nil {
		return
	}
	_ = writeFileAtomic(filepath.Join(dir, appliedUnitsFile), b)
}

// unitsFor derives the applied-units record that DESCRIBES a desired state as
// if it had been fully applied (used to seed from a legacy applied-state file
// or from the installer's baseline compose).
func unitsFor(ds setup.DesiredState) appliedUnits {
	a := appliedUnits{Compose: composeHash(ds), DB: dbHash(ds), App: appHash(ds), Traefik: traefikHash(ds)}
	if ds.PullBeforeApply {
		a.AppPullGeneration = ds.Generation
	}
	return a
}

func hashJSON(v any) string {
	b, _ := json.Marshal(v)
	sum := sha256.Sum256(b)
	return fmt.Sprintf("%x", sum[:8])
}

// composeHash covers the whole generated file and the .env keys it reads.
func composeHash(ds setup.DesiredState) string {
	return hashJSON(struct {
		Compose  string
		Image    string
		HTTPPort string
		RaftPort string
	}{setup.BuildComposeContent(ds), ds.Image, ds.HTTPPort, ds.RaftPort})
}

// dbHash: the managed Postgres provision, or "absent".
func dbHash(ds setup.DesiredState) string {
	if ds.DBMode != setup.DBModePostgresManaged {
		return "absent"
	}
	return hashJSON(ds.DB)
}

// appHash: everything in the app's compose stanza / env that needs a recreate
// to take effect. Generation and PullBeforeApply are deliberately NOT part of
// it (see appliedUnits.AppPullGeneration).
func appHash(ds setup.DesiredState) string {
	return hashJSON(struct {
		Image      string
		DBMode     string
		DBDSN      string
		HTTPPort   string
		RaftPort   string
		BackupPath string
	}{ds.Image, ds.DBMode, ds.DBDSN, ds.HTTPPort, ds.RaftPort, ds.BackupHostPath})
}

// traefikHash: the gateway provision, or "absent".
func traefikHash(ds setup.DesiredState) string {
	if !ds.GatewayEnabled() {
		return "absent"
	}
	return hashJSON(ds.Gateway)
}

// pullPending reports whether the desired state carries a pull request the
// app unit has not executed yet.
func pullPending(ds setup.DesiredState, applied appliedUnits) bool {
	return ds.PullBeforeApply && ds.Generation > applied.AppPullGeneration
}

// unitState is the in-memory reconcile state of one unit.
type unitState struct {
	phase     string
	message   string
	err       string
	failures  int
	nextRetry time.Time
	updatedAt time.Time
}

// Retry back-off: 2s, 4s, 8s … capped at a minute — quick enough that the
// gateway comes up within seconds of the clashing proxy being stopped, slow
// enough that a permanently failing pull does not spam the daemon.
const (
	retryBase = 2 * time.Second
	retryMax  = time.Minute
)

func backoff(failures int) time.Duration {
	if failures <= 0 {
		return 0
	}
	d := retryBase
	for i := 1; i < failures && d < retryMax; i++ {
		d *= 2
	}
	if d > retryMax {
		d = retryMax
	}
	return d
}

// --- per-unit apply ------------------------------------------------------------

// applyCompose regenerates docker-compose.yml and syncs the stack .env keys the
// generated file reads through variable substitution.
func (c *controller) applyCompose(ds setup.DesiredState) (string, error) {
	compose := setup.BuildComposeContent(ds)
	if err := writeFileAtomic(filepath.Join(c.stackDir, "docker-compose.yml"), []byte(compose)); err != nil {
		return "", fmt.Errorf("write compose: %w", err)
	}
	// The generated compose references the image and published ports through
	// stack-.env vars (image: ${NODE_STATS_IMAGE:-…}); a SET value there SHADOWS
	// the compose default, so a pinned NODE_STATS_IMAGE (:latest) would defeat a
	// channel switch / "update now" setting ds.Image=:beta. Only the controller
	// writes the stack .env — sync the relevant keys before anything is (re)created.
	envPath := filepath.Join(c.stackDir, ".env")
	if img := strings.TrimSpace(ds.Image); img != "" {
		if err := upsertEnvKey(envPath, "NODE_STATS_IMAGE", img); err != nil {
			return "", fmt.Errorf("update stack .env (NODE_STATS_IMAGE): %w", err)
		}
	}
	if strings.TrimSpace(ds.HTTPPort) != "" {
		if err := upsertEnvKey(envPath, "NODE_STATS_PORT", ds.HTTPPort); err != nil {
			return "", fmt.Errorf("update stack .env (NODE_STATS_PORT): %w", err)
		}
	}
	if strings.TrimSpace(ds.RaftPort) != "" {
		if err := upsertEnvKey(envPath, "NODE_STATS_RAFT_PORT", ds.RaftPort); err != nil {
			return "", fmt.Errorf("update stack .env (NODE_STATS_RAFT_PORT): %w", err)
		}
	}
	return "compose regenerated", nil
}

// applyDB brings the managed Postgres up (waiting for its healthcheck) or
// tears the orphaned service down when the mode moved away from managed.
func (c *controller) applyDB(ds setup.DesiredState) (string, error) {
	if ds.DBMode == setup.DBModePostgresManaged {
		if out, err := c.run.compose("up", "-d", "--wait", "db"); err != nil {
			return "", fmt.Errorf("start db: %s: %w", tailLines(out, 6), err)
		}
		return "db running", nil
	}
	// Best-effort teardown (--remove-orphans on the app recreate also catches
	// it, but an explicit stop+rm is clearer and survives `db` still being
	// referenced by a stale override). No-such-service is fine.
	if out, err := c.run.compose("stop", "db"); err != nil {
		log.Debug("controller: stop db (ignored)", "out", out, "error", err)
	}
	if out, err := c.run.compose("rm", "-f", "db"); err != nil {
		log.Debug("controller: rm db (ignored)", "out", out, "error", err)
	}
	return "no managed db", nil
}

// applyApp pulls when a pull is pending and recreates ONLY the app service:
// --no-deps keeps the controller (and db, traefik) untouched; --force-recreate
// restarts the app even when its stanza is unchanged so it re-reads the
// bind-mounted .env.agent; --remove-orphans drops services no longer in the
// generated compose. Returns whether a pull happened.
func (c *controller) applyApp(ds setup.DesiredState, pull bool) (string, bool, error) {
	if pull {
		if out, err := c.run.compose("pull", c.appService); err != nil {
			return "", false, fmt.Errorf("pull %s: %s: %w", c.appService, tailLines(out, 6), err)
		}
	}
	if out, err := c.run.compose("up", "-d", "--no-deps", "--force-recreate", "--remove-orphans", c.appService); err != nil {
		return "", pull, fmt.Errorf("recreate %s: %s: %w", c.appService, tailLines(out, 6), err)
	}
	msg := "recreated " + c.appService
	if pull {
		msg = "pulled and recreated " + c.appService
		// Reclaim the now-dangling old image layers the pull replaced. Dangling-
		// only (no `-a`), so it never removes images still referenced by a service.
		if out, err := c.run.docker("image", "prune", "-f"); err != nil {
			log.Debug("controller: image prune (ignored)", "out", out, "error", err)
		} else {
			log.Info("controller: pruned dangling images")
		}
	}
	return msg, pull, nil
}

// applyTraefik brings the managed gateway up (its ping healthcheck gates
// --wait, so a port clash surfaces here) or removes it when disabled.
func (c *controller) applyTraefik(ds setup.DesiredState) (string, error) {
	if ds.GatewayEnabled() {
		if out, err := c.run.compose("up", "-d", "--wait", "traefik"); err != nil {
			return "", fmt.Errorf("start traefik (gateway): %s: %w", tailLines(out, 6), err)
		}
		return "traefik running", nil
	}
	if out, err := c.run.compose("stop", "traefik"); err != nil {
		log.Debug("controller: stop traefik (ignored)", "out", out, "error", err)
	}
	if out, err := c.run.compose("rm", "-f", "traefik"); err != nil {
		log.Debug("controller: rm traefik (ignored)", "out", out, "error", err)
	}
	return "gateway disabled", nil
}
