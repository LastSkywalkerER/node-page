package appbackup

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/charmbracelet/log"
)

// RunJobCLI is the `node-stats appjob <job.json> <result.json>` subcommand: the
// body of the short-lived helper container the controller launches.
//
// It runs in a container where every path the job declared is identity-mounted,
// which is the whole reason the helper exists — see controller/appjobs.go. The
// outcome is written to resultPath rather than signalled through the exit code
// alone, so the controller can report a partial success (snapshot taken, later
// step failed) instead of just "it broke".
func RunJobCLI(args []string) {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: node-stats appjob <job.json> <result.json>")
		os.Exit(2)
	}
	jobPath, resultPath := args[0], args[1]

	var job Job
	b, err := os.ReadFile(jobPath)
	if err == nil {
		err = json.Unmarshal(b, &job)
	}
	if err != nil {
		writeResult(resultPath, Result{Error: fmt.Sprintf("read job %s: %v", jobPath, err)})
		log.Error("appjob: unreadable job descriptor", "path", jobPath, "error", err)
		os.Exit(1)
	}

	bin, err := EnsureBinary(context.Background(), resticDirOf(jobPath))
	if err != nil {
		writeResult(resultPath, Result{JobID: job.ID, Error: "restic unavailable: " + err.Error()})
		log.Error("appjob: restic unavailable", "error", err)
		os.Exit(1)
	}

	host, _ := os.Hostname()
	ex := &Executor{
		Job:      job,
		Restic:   Runner{Bin: bin, Repo: job.Repo, Port: 0},
		Hostname: host,
		Emit: func(step, msg string) {
			log.Info("appjob", "id", job.ID, "kind", job.Kind, "project", job.Project, "step", step, "message", msg)
		},
	}

	// A generous ceiling: a first full snapshot of a large application is
	// legitimately slow, and the controller is not waiting on a timer.
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Hour)
	defer cancel()

	snapID, runErr := ex.Run(ctx)
	res := Result{JobID: job.ID, SnapshotID: snapID, FinishedAt: time.Now().UTC()}
	if runErr != nil {
		res.Error = runErr.Error()
	}
	writeResult(resultPath, res)
	if runErr != nil {
		log.Error("appjob: failed", "id", job.ID, "error", runErr)
		os.Exit(1)
	}
	log.Info("appjob: done", "id", job.ID, "snapshot", snapID)
}

// resticDirOf derives the shared data directory from the job descriptor's
// location (<dataDir>/appjobs/<id>.json), so the helper finds the restic binary
// the app installed without needing the environment to agree.
func resticDirOf(jobPath string) string {
	if d := os.Getenv("NODE_STATS_DATA_DIR"); d != "" {
		return d
	}
	// <dataDir>/appjobs/<id>.json → <dataDir>
	return parentOf(parentOf(jobPath))
}

func parentOf(p string) string {
	for i := len(p) - 1; i > 0; i-- {
		if p[i] == '/' {
			return p[:i]
		}
	}
	return "."
}

func writeResult(path string, res Result) {
	if res.FinishedAt.IsZero() {
		res.FinishedAt = time.Now().UTC()
	}
	b, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(path, b, 0o600)
}
