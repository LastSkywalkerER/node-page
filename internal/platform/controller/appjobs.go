package controller

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/charmbracelet/log"

	"system-stats/internal/platform/appbackup"
	"system-stats/internal/platform/setup"
)

// Application backup / update / restore jobs.
//
// The controller does NOT do this work itself. It mounts only the docker socket
// and its own stack directory, so it cannot read another project's compose file
// or its data — and it must not gain a permanent host-root mount just in case.
// Instead each job runs in a short-lived helper container (the same image, the
// `appjob` subcommand) with identity mounts for exactly the paths that job
// declared: the same path inside the container as on the host, because
// `docker compose` resolves a project's relative bind mounts locally and hands
// the daemon absolute paths.
//
// Jobs run one at a time, oldest first: they stop containers, so overlapping
// two jobs would mean two of them fighting over one project.

const appJobHelperPrefix = "node-stats-appjob-"

// appJobs drains at most one pending job per tick.
func (c *controller) appJobs() {
	q, err := appbackup.ReadJobs(c.dataDir)
	if err != nil {
		log.Error("controller: read app-jobs", "error", err)
		return
	}
	if q == nil || len(q.Jobs) == 0 {
		return
	}
	sf, err := appbackup.ReadStatus(c.dataDir)
	if err != nil {
		log.Error("controller: read app-jobs-status", "error", err)
		return
	}
	if sf == nil {
		sf = &appbackup.StatusFile{Statuses: map[string]appbackup.JobStatus{}}
	}
	pending := q.Pending(sf.Statuses)
	if len(pending) == 0 {
		return
	}
	c.runAppJob(pending[0], q.Generation, sf)
}

// runAppJob executes one job in a helper container and records the outcome.
func (c *controller) runAppJob(job appbackup.Job, generation int, sf *appbackup.StatusFile) {
	started := c.now().UTC()
	c.putJobStatus(sf, generation, appbackup.JobStatus{
		ID: job.ID, Phase: appbackup.PhaseRunning,
		Step: "starting", Message: "launching helper for " + job.Project,
		StartedAt: started,
	})

	res, out, err := c.execAppJob(job)
	finished := c.now().UTC()

	st := appbackup.JobStatus{
		ID: job.ID, StartedAt: started, FinishedAt: finished,
		Log: tailNonEmpty(out, 60),
	}
	switch {
	case err != nil:
		st.Phase = appbackup.PhaseFailed
		st.Error = err.Error()
		if res != nil && res.Error != "" {
			st.Error = res.Error
		}
		log.Error("controller: app job failed", "id", job.ID, "kind", job.Kind, "project", job.Project, "error", st.Error)
	case res != nil && res.Error != "":
		st.Phase = appbackup.PhaseFailed
		st.Error = res.Error
		st.SnapshotID = res.SnapshotID
		log.Error("controller: app job failed", "id", job.ID, "kind", job.Kind, "project", job.Project, "error", res.Error)
	default:
		st.Phase = appbackup.PhaseSucceeded
		st.Step = "done"
		if res != nil {
			st.SnapshotID = res.SnapshotID
		}
		st.Message = fmt.Sprintf("%s of %s completed", job.Kind, job.Project)
		log.Info("controller: app job done", "id", job.ID, "kind", job.Kind, "project", job.Project, "snapshot", st.SnapshotID)
	}
	c.putJobStatus(sf, generation, st)
}

// execAppJob writes the job descriptor next to the shared state, runs the
// helper and reads back its result.
func (c *controller) execAppJob(job appbackup.Job) (*appbackup.Result, string, error) {
	jobDir := filepath.Join(c.dataDir, "appjobs")
	if err := os.MkdirAll(jobDir, 0o700); err != nil {
		return nil, "", err
	}
	jobPath := filepath.Join(jobDir, job.ID+".json")
	resPath := filepath.Join(jobDir, job.ID+".result.json")
	// The descriptor carries the repository password, exactly as
	// desired-state.json already carries the database DSN: 0600 on the app's
	// own data volume, removed as soon as the helper is done.
	b, err := json.MarshalIndent(job, "", "  ")
	if err != nil {
		return nil, "", err
	}
	if err := os.WriteFile(jobPath, b, 0o600); err != nil {
		return nil, "", err
	}
	defer func() {
		os.Remove(jobPath)
		os.Remove(resPath)
	}()

	name := appJobHelperPrefix + job.ID
	// Clear a helper left behind by a previous crash.
	_, _ = c.run.docker("rm", "-f", name)

	args := []string{"run", "--rm", "--name", name,
		"-v", "/var/run/docker.sock:/var/run/docker.sock",
	}
	for _, m := range appJobMounts(job, c.dataDir) {
		args = append(args, "-v", m+":"+m)
	}
	args = append(args, c.appJobImage(), "appjob", jobPath, resPath)

	out, runErr := c.run.docker(args...)

	var res appbackup.Result
	if rb, err := os.ReadFile(resPath); err == nil {
		if json.Unmarshal(rb, &res) == nil {
			return &res, out, runErr
		}
	}
	if runErr != nil {
		return nil, out, fmt.Errorf("helper container failed: %s", tailLines(out, 3))
	}
	return nil, out, fmt.Errorf("helper produced no result file")
}

// appJobMounts is the deduplicated, sorted set of host paths the helper needs
// identity-mounted: the shared data dir (job descriptor + the restic binary),
// the project directory, each compose file's directory and each data location.
//
// Parents absorb children so `docker run` is not handed two overlapping
// mounts of the same tree.
func appJobMounts(job appbackup.Job, dataDir string) []string {
	set := map[string]bool{}
	add := func(p string) {
		p = strings.TrimSpace(p)
		if p == "" || p == "/" || !filepath.IsAbs(p) {
			return
		}
		set[filepath.Clean(p)] = true
	}
	add(dataDir)
	add(job.ProjectDir)
	// A filesystem repository lives outside every other declared path, so it
	// needs its own mount or the helper cannot write the snapshot.
	if d := localRepoDir(job.Repo.URL); d != "" {
		add(d)
	}
	for _, f := range job.ComposeFiles {
		add(filepath.Dir(f))
	}
	for _, p := range job.Paths {
		add(p.Source)
	}

	var all []string
	for p := range set {
		all = append(all, p)
	}
	sort.Strings(all)

	var out []string
	for _, p := range all {
		covered := false
		for _, kept := range out {
			if p == kept || strings.HasPrefix(p, kept+"/") {
				covered = true
				break
			}
		}
		if !covered {
			out = append(out, p)
		}
	}
	return out
}

// localRepoDir returns the directory of a filesystem restic repository, or ""
// for a remote backend (sftp:, s3:, rest:, …), which needs no mount.
func localRepoDir(url string) string {
	u := strings.TrimSpace(url)
	if u == "" || !filepath.IsAbs(u) {
		return ""
	}
	return filepath.Clean(u)
}

// appJobImage is the image the helper runs — the same one this controller was
// started from, so a job never runs a different node-stats version than the
// node it belongs to.
func (c *controller) appJobImage() string {
	if c.jobImage != "" {
		return c.jobImage
	}
	if v := strings.TrimSpace(os.Getenv("NODE_STATS_IMAGE")); v != "" {
		return v
	}
	return setup.DefaultImage
}

// putJobStatus merges one status into the status file and persists it.
func (c *controller) putJobStatus(sf *appbackup.StatusFile, generation int, st appbackup.JobStatus) {
	if sf.Statuses == nil {
		sf.Statuses = map[string]appbackup.JobStatus{}
	}
	// Keep an earlier snapshot id / start time when a later write omits them.
	if prev, ok := sf.Statuses[st.ID]; ok {
		if st.SnapshotID == "" {
			st.SnapshotID = prev.SnapshotID
		}
		if st.StartedAt.IsZero() {
			st.StartedAt = prev.StartedAt
		}
	}
	sf.Statuses[st.ID] = st
	sf.Generation = generation
	if err := appbackup.WriteStatus(c.dataDir, *sf); err != nil {
		log.Error("controller: write app-jobs-status", "error", err)
	}
}

// tailNonEmpty keeps the last n non-empty output lines for the UI.
func tailNonEmpty(s string, n int) []string {
	var lines []string
	for _, l := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(l); t != "" {
			lines = append(lines, t)
		}
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}
