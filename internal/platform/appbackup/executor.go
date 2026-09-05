package appbackup

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Executor runs one job to completion. It executes inside the short-lived
// helper container the controller launches, where every path the job declared
// is identity-mounted — the same path inside as on the host — because
// `docker compose` resolves a project's relative bind mounts locally and hands
// the daemon absolute paths. A translated prefix (/host/...) would make compose
// create bind mounts at the wrong place on the real host.
type Executor struct {
	Job      Job
	Restic   Runner
	Hostname string

	// Emit reports progress; the controller forwards it to the status file.
	Emit func(step, message string)
}

// Run dispatches on the job kind.
func (e *Executor) Run(ctx context.Context) (snapshotID string, err error) {
	switch e.Job.Kind {
	case JobBackup:
		return e.backup(ctx)
	case JobUpdate:
		return e.update(ctx)
	case JobRestore:
		return "", e.restore(ctx)
	}
	return "", fmt.Errorf("unknown job kind %q", e.Job.Kind)
}

func (e *Executor) step(step, msg string) {
	if e.Emit != nil {
		e.Emit(step, msg)
	}
}

// --- job kinds ---------------------------------------------------------------

// backup stops the project, snapshots it cold and starts it again. Stopping is
// what makes the snapshot trustworthy: a database copied while it is running is
// not a backup, and this is the one moment we are allowed to stop things.
func (e *Executor) backup(ctx context.Context) (string, error) {
	if err := e.Restic.EnsureRepo(ctx); err != nil {
		return "", err
	}

	e.step("stop", "stopping "+e.Job.Project)
	if out, err := e.compose(ctx, "stop"); err != nil {
		return "", fmt.Errorf("compose stop: %s: %w", firstLines(out, 3), err)
	}

	id, err := e.snapshot(ctx, JobBackup, e.currentComposeImages())
	// Always try to bring the project back up, even if the snapshot failed —
	// leaving an application down because a backup broke is the worse outcome.
	e.step("start", "starting "+e.Job.Project)
	if out, upErr := e.compose(ctx, "up", "-d"); upErr != nil {
		if err == nil {
			return id, fmt.Errorf("compose up: %s: %w", firstLines(out, 3), upErr)
		}
		return id, fmt.Errorf("%w (and compose up also failed: %s)", err, firstLines(out, 2))
	}
	return id, err
}

// update snapshots first, then rewrites the compose file to the requested image
// references, pulls and brings the project back up. It deliberately does not
// judge the result: the operator inspects the application and rolls back
// explicitly if unhappy, which is why the snapshot is taken unconditionally.
func (e *Executor) update(ctx context.Context) (string, error) {
	if len(e.Job.Targets) == 0 {
		return "", fmt.Errorf("update job carries no service targets")
	}
	if err := e.Restic.EnsureRepo(ctx); err != nil {
		return "", err
	}

	e.step("stop", "stopping "+e.Job.Project)
	if out, err := e.compose(ctx, "stop"); err != nil {
		return "", fmt.Errorf("compose stop: %s: %w", firstLines(out, 3), err)
	}

	// The snapshot records the images that were in the file, so a restore can
	// put both the data AND the pinned versions back.
	id, err := e.snapshot(ctx, JobUpdate, e.currentComposeImages())
	if err != nil {
		// Do not touch the compose file when we have no way back.
		e.step("start", "backup failed — restarting unchanged")
		_, _ = e.compose(ctx, "up", "-d")
		return "", err
	}

	e.step("rewrite", "rewriting image references")
	if err := e.rewrite(); err != nil {
		e.step("start", "rewrite failed — restarting unchanged")
		_, _ = e.compose(ctx, "up", "-d")
		return id, err
	}

	e.step("pull", "pulling images")
	if out, err := e.compose(ctx, "pull"); err != nil {
		return id, fmt.Errorf("compose pull: %s: %w", firstLines(out, 5), err)
	}

	e.step("start", "starting "+e.Job.Project)
	if out, err := e.compose(ctx, "up", "-d"); err != nil {
		return id, fmt.Errorf("compose up: %s: %w", firstLines(out, 5), err)
	}
	return id, nil
}

// restore is update in reverse: it takes the project down, erases its current
// data, writes the chosen snapshot back over it — compose file included, so the
// old image tags return with the data — and starts the project again.
func (e *Executor) restore(ctx context.Context) error {
	if e.Job.SnapshotID == "" {
		return fmt.Errorf("restore job carries no snapshot id")
	}

	// Refuse before stopping anything if any declared path is unsafe to erase.
	for _, p := range e.Job.Paths {
		if err := CheckWipable(p.Source); err != nil {
			return fmt.Errorf("refusing to restore: %w", err)
		}
	}

	e.step("down", "stopping and removing "+e.Job.Project)
	// `down` without -v: containers and networks go, named volumes stay, so
	// their data directories keep existing for the restore to write into.
	if out, err := e.compose(ctx, "down"); err != nil {
		return fmt.Errorf("compose down: %s: %w", firstLines(out, 3), err)
	}

	e.step("wipe", fmt.Sprintf("erasing current data (%d locations)", len(e.Job.Paths)))
	for _, p := range e.Job.Paths {
		if err := WipeContents(p.Source); err != nil {
			return fmt.Errorf("wipe %s: %w", p.Source, err)
		}
	}

	e.step("restore", "restoring snapshot "+shortID(e.Job.SnapshotID))
	// Snapshot paths are absolute and contain only what we backed up, so
	// restoring to / puts every file back exactly where it came from.
	if out, err := e.Restic.Run(ctx, "restore", e.Job.SnapshotID, "--target", "/"); err != nil {
		return fmt.Errorf("restic restore: %s: %w", firstLines(out, 5), err)
	}

	e.step("start", "starting "+e.Job.Project)
	if out, err := e.compose(ctx, "up", "-d"); err != nil {
		return fmt.Errorf("compose up: %s: %w", firstLines(out, 5), err)
	}
	return nil
}

// --- helpers -----------------------------------------------------------------

// snapshot backs up the compose files plus every declared data location, tagged
// so the UI can list one project's history and a restore knows what it holds.
func (e *Executor) snapshot(ctx context.Context, kind string, images map[string]string) (string, error) {
	// On an update, record WHAT is about to change as well as the state being
	// left: the history should say "nginx 1.27 → 1.31", not merely list every
	// image the project happened to run.
	var moves []SnapshotMove
	if kind == JobUpdate {
		for _, t := range e.Job.Targets {
			from := images[t.Service]
			if from == "" {
				from = t.CurrentImage
			}
			moves = append(moves, SnapshotMove{Service: t.Service, From: from, To: t.TargetImage})
		}
	}
	targets := e.backupTargets()
	if len(targets) == 0 {
		return "", fmt.Errorf("nothing to back up: no compose files and no data locations resolved")
	}

	args := []string{"backup", "--json"}
	for _, t := range BuildTags(e.Job.Project, kind, e.Hostname, images, moves) {
		args = append(args, "--tag", t)
	}
	args = append(args, targets...)

	e.step("backup", fmt.Sprintf("snapshotting %d locations", len(targets)))
	out, err := e.Restic.Run(ctx, args...)
	if err != nil {
		return "", fmt.Errorf("restic backup: %s: %w", firstLines(out, 5), err)
	}
	id := parseSnapshotID(out)
	if id == "" {
		return "", fmt.Errorf("restic backup produced no snapshot id: %s", firstLines(out, 3))
	}
	return id, nil
}

// backupTargets is the deduplicated, existing set of paths to snapshot: the
// compose files, a sibling .env when present, and each data location.
func (e *Executor) backupTargets() []string {
	seen := map[string]bool{}
	var out []string
	add := func(p string) {
		if p == "" || seen[p] {
			return
		}
		if _, err := os.Stat(p); err != nil {
			return
		}
		seen[p] = true
		out = append(out, p)
	}
	for _, f := range e.Job.ComposeFiles {
		add(f)
		add(filepath.Join(filepath.Dir(f), ".env"))
	}
	for _, p := range e.Job.Paths {
		add(p.Source)
	}
	return out
}

// currentComposeImages reads the image reference each service declares right
// now, so the snapshot records the state we are leaving.
func (e *Executor) currentComposeImages() map[string]string {
	merged := map[string]string{}
	for _, f := range e.Job.ComposeFiles {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		for svc, img := range ComposeImages(string(b)) {
			merged[svc] = img
		}
	}
	return merged
}

// rewrite applies the job's targets to whichever compose file declares each
// service. A service nobody declares is an error: silently updating nothing
// would leave the operator believing the version changed.
func (e *Executor) rewrite() error {
	want := map[string]string{}
	for _, t := range e.Job.Targets {
		want[t.Service] = t.TargetImage
	}

	unresolved := map[string]bool{}
	for svc := range want {
		unresolved[svc] = true
	}
	for _, f := range e.Job.ComposeFiles {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		here := map[string]string{}
		for svc := range ComposeImages(string(b)) {
			if img, ok := want[svc]; ok {
				here[svc] = img
			}
		}
		if len(here) == 0 {
			continue
		}
		if _, err := RewriteComposeFile(f, here); err != nil {
			return err
		}
		for svc := range here {
			delete(unresolved, svc)
		}
	}
	if len(unresolved) > 0 {
		var names []string
		for svc := range unresolved {
			names = append(names, svc)
		}
		return fmt.Errorf("services not found in any compose file: %s", strings.Join(names, ", "))
	}
	return nil
}

// compose runs `docker compose` against the job's project.
func (e *Executor) compose(ctx context.Context, args ...string) (string, error) {
	full := []string{"compose", "-p", e.Job.Project}
	for _, f := range e.Job.ComposeFiles {
		full = append(full, "-f", f)
	}
	if e.Job.ProjectDir != "" {
		full = append(full, "--project-directory", e.Job.ProjectDir)
	}
	full = append(full, args...)

	cctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(cctx, "docker", full...)
	if e.Job.ProjectDir != "" {
		cmd.Dir = e.Job.ProjectDir
	}
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// parseSnapshotID pulls the snapshot id out of `restic backup --json`, whose
// output is a stream of JSON objects ending in a summary.
func parseSnapshotID(out string) string {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var m struct {
			MessageType string `json:"message_type"`
			SnapshotID  string `json:"snapshot_id"`
		}
		if json.Unmarshal([]byte(line), &m) != nil {
			continue
		}
		if m.SnapshotID != "" {
			return m.SnapshotID
		}
	}
	return ""
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
