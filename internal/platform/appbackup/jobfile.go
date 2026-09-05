package appbackup

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Files exchanged between the app and the controller on the shared data volume
// (NODE_STATS_DATA_DIR). Deliberately separate from desired-state.json: that
// descriptor is node-stats' own stack, this queue is other people's
// applications, and conflating them would let an application job block an
// infrastructure change (or the reverse).
const (
	JobsFile      = "app-jobs.json"
	JobStatusFile = "app-jobs-status.json"
)

// maxLogLines bounds the command output kept per job so the status file cannot
// grow without limit on a chatty pull.
const maxLogLines = 200

// JobQueue is the app→controller request file. Jobs are executed oldest first,
// one at a time — these operations stop containers, so overlapping them would
// mean two jobs fighting over the same project.
type JobQueue struct {
	// Generation increments on every write so the controller can cheaply skip
	// a file it has already fully drained.
	Generation int   `json:"generation"`
	Jobs       []Job `json:"jobs"`
}

// StatusFile is the controller→app result file, keyed by job id.
type StatusFile struct {
	Generation int                  `json:"generation"`
	Statuses   map[string]JobStatus `json:"statuses"`
	UpdatedAt  time.Time            `json:"updated_at"`
}

// Pending returns the queued jobs in execution order (oldest first).
func (q JobQueue) Pending(done map[string]JobStatus) []Job {
	var out []Job
	for _, j := range q.Jobs {
		st, ok := done[j.ID]
		if ok && (st.Phase == PhaseSucceeded || st.Phase == PhaseFailed) {
			continue
		}
		out = append(out, j)
	}
	sort.Slice(out, func(i, k int) bool { return out[i].CreatedAt.Before(out[k].CreatedAt) })
	return out
}

// AppendJob adds a job to the queue file, bumping the generation. It is the
// only writer of JobsFile; the controller only reads it.
func AppendJob(dir string, job Job) error {
	q, err := ReadJobs(dir)
	if err != nil {
		return err
	}
	if q == nil {
		q = &JobQueue{}
	}
	q.Generation++
	q.Jobs = append(q.Jobs, job)
	// Keep the queue from growing forever: drop entries older than a week, the
	// history lives in the database.
	cutoff := time.Now().Add(-7 * 24 * time.Hour)
	kept := q.Jobs[:0]
	for _, j := range q.Jobs {
		if j.CreatedAt.After(cutoff) {
			kept = append(kept, j)
		}
	}
	q.Jobs = kept
	return writeJSONAtomic(filepath.Join(dir, JobsFile), q, 0o600)
}

// ReadJobs loads the queue; returns (nil, nil) when the file does not exist.
func ReadJobs(dir string) (*JobQueue, error) {
	var q JobQueue
	ok, err := readJSON(filepath.Join(dir, JobsFile), &q)
	if err != nil || !ok {
		return nil, err
	}
	return &q, nil
}

// ReadStatus loads the controller's results; returns (nil, nil) when absent.
func ReadStatus(dir string) (*StatusFile, error) {
	var s StatusFile
	ok, err := readJSON(filepath.Join(dir, JobStatusFile), &s)
	if err != nil || !ok {
		return nil, err
	}
	if s.Statuses == nil {
		s.Statuses = map[string]JobStatus{}
	}
	return &s, nil
}

// WriteStatus atomically replaces the status file. Written by the controller.
func WriteStatus(dir string, s StatusFile) error {
	s.UpdatedAt = time.Now().UTC()
	for id, st := range s.Statuses {
		if len(st.Log) > maxLogLines {
			st.Log = st.Log[len(st.Log)-maxLogLines:]
			s.Statuses[id] = st
		}
	}
	return writeJSONAtomic(filepath.Join(dir, JobStatusFile), s, 0o600)
}

func readJSON(path string, v any) (bool, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	if err := json.Unmarshal(b, v); err != nil {
		return false, err
	}
	return true, nil
}

// writeJSONAtomic writes via temp file + rename so a reader never sees a
// partially written file.
func writeJSONAtomic(path string, v any, mode os.FileMode) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, mode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
