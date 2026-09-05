package appbackup

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// End-to-end exercise of the executor against a real docker daemon and a real
// restic binary: a throwaway compose project is created, backed up, damaged,
// restored and updated. It is the only test that proves the pieces work
// together — the unit tests above only prove each piece works alone.
//
// Opt-in, because it pulls images and downloads restic:
//
//	NODE_STATS_E2E=1 go test ./internal/platform/appbackup/ -run E2E -v
func requireE2E(t *testing.T) {
	t.Helper()
	if os.Getenv("NODE_STATS_E2E") == "" {
		t.Skip("set NODE_STATS_E2E=1 to run the docker end-to-end test")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not available")
	}
	out, err := exec.Command("docker", "compose", "version").CombinedOutput()
	if err != nil {
		t.Skipf("docker compose unavailable: %s", out)
	}
}

const e2eComposeTemplate = `# throwaway stack for the node-stats end-to-end test
services:
    keeper:
        image: %s
        container_name: %s
        restart: "no"
        command: ["sh", "-c", "sleep 3600"]   # keep it simple
        volumes:
            - %s:/data
`

// e2eStack materialises a compose project in a temp dir with a bind-mounted
// data directory, and returns everything the executor needs.
type e2eStack struct {
	dir      string
	project  string
	compose  string
	dataDir  string
	contName string
}

func newE2EStack(t *testing.T, image string) *e2eStack {
	t.Helper()
	// A short, unique project name: docker object names have limits and a
	// leftover from a previous run must not collide.
	suffix := fmt.Sprintf("%d", time.Now().UnixNano()%1e9)
	s := &e2eStack{
		project:  "nsbk" + suffix,
		contName: "nsbk-keeper-" + suffix,
	}
	// t.TempDir() on macOS lives under /var/folders/... which docker Desktop
	// does not share by default; use a path under the user's home instead.
	base, err := os.MkdirTemp(os.Getenv("HOME"), "ns-e2e-")
	if err != nil {
		t.Fatal(err)
	}
	s.dir = base
	s.dataDir = filepath.Join(base, "data")
	s.compose = filepath.Join(base, "docker-compose.yml")
	if err := os.MkdirAll(s.dataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(e2eComposeTemplate, image, s.contName, s.dataDir)
	if err := os.WriteFile(s.compose, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		_ = exec.Command("docker", "compose", "-p", s.project, "-f", s.compose, "down", "-v", "--remove-orphans").Run()
		_ = os.RemoveAll(base)
	})
	return s
}

func (s *e2eStack) job(kind string, repo ResolvedRepo) Job {
	return Job{
		ID:           "e2e-" + kind,
		Kind:         kind,
		Project:      s.project,
		ProjectDir:   s.dir,
		ComposeFiles: []string{s.compose},
		Paths:        []BackupPath{{Kind: "bind", Source: s.dataDir}},
		Repo:         repo,
		CreatedAt:    time.Now().UTC(),
	}
}

func (s *e2eStack) up(t *testing.T) {
	t.Helper()
	out, err := exec.Command("docker", "compose", "-p", s.project, "-f", s.compose,
		"--project-directory", s.dir, "up", "-d").CombinedOutput()
	if err != nil {
		t.Fatalf("compose up: %s: %v", out, err)
	}
}

func (s *e2eStack) running(t *testing.T) bool {
	t.Helper()
	out, _ := exec.Command("docker", "compose", "-p", s.project, "-f", s.compose, "ps", "-q").Output()
	return strings.TrimSpace(string(out)) != ""
}

func (s *e2eStack) runningImage(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("docker", "inspect", "--format", "{{.Config.Image}}", s.contName).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// e2eRestic installs restic into a temp dir and points it at a fresh local
// repository.
func e2eRestic(t *testing.T) (Runner, ResolvedRepo) {
	t.Helper()
	home, err := os.MkdirTemp(os.Getenv("HOME"), "ns-e2e-restic-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(home) })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	bin, err := EnsureBinary(ctx, home)
	if err != nil {
		t.Fatalf("install restic: %v", err)
	}
	repo := Resolve(
		RepoConfig{Backend: BackendLocal, Path: filepath.Join(home, "repo")},
		RepoSecrets{Password: "e2e-test-password"},
	)
	return Runner{Bin: bin, Repo: repo}, repo
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

// TestE2EBackupAndRestore is the important one: real containers are stopped,
// real bytes are snapshotted, the data is then destroyed and must come back.
func TestE2EBackupAndRestore(t *testing.T) {
	requireE2E(t)
	stack := newE2EStack(t, "busybox:1.36")
	runner, repo := e2eRestic(t)

	writeFile(t, filepath.Join(stack.dataDir, "important.txt"), "the original bytes\n")
	stack.up(t)
	if !stack.running(t) {
		t.Fatal("stack did not start")
	}

	ctx := context.Background()
	var steps []string
	emit := func(step, msg string) { steps = append(steps, step); t.Logf("  [%s] %s", step, msg) }

	// --- backup ---------------------------------------------------------------
	ex := &Executor{Job: stack.job(JobBackup, repo), Restic: runner, Hostname: "e2e", Emit: emit}
	snapID, err := ex.Run(ctx)
	if err != nil {
		t.Fatalf("backup: %v", err)
	}
	if snapID == "" {
		t.Fatal("backup produced no snapshot id")
	}
	if !stack.running(t) {
		t.Fatal("backup left the application stopped")
	}
	if want := []string{"stop", "backup", "start"}; !reflect_DeepEqualStrings(steps, want) {
		t.Errorf("backup steps = %v, want %v", steps, want)
	}

	snaps, err := runner.Snapshots(ctx, ProjectTag(stack.project))
	if err != nil {
		t.Fatalf("snapshots: %v", err)
	}
	if len(snaps) != 1 {
		t.Fatalf("snapshots = %d, want 1", len(snaps))
	}
	if snaps[0].Kind != JobBackup || snaps[0].Project != stack.project {
		t.Errorf("snapshot tags wrong: %#v", snaps[0])
	}
	if snaps[0].Images["keeper"] != "busybox:1.36" {
		t.Errorf("images tag = %#v, want keeper=busybox:1.36", snaps[0].Images)
	}

	// --- destroy the data, then restore --------------------------------------
	writeFile(t, filepath.Join(stack.dataDir, "important.txt"), "CORRUPTED\n")
	writeFile(t, filepath.Join(stack.dataDir, "junk.txt"), "should not survive\n")

	steps = nil
	restoreJob := stack.job(JobRestore, repo)
	restoreJob.SnapshotID = snapID
	ex = &Executor{Job: restoreJob, Restic: runner, Hostname: "e2e", Emit: emit}
	if _, err := ex.Run(ctx); err != nil {
		t.Fatalf("restore: %v", err)
	}

	if got := readFile(t, filepath.Join(stack.dataDir, "important.txt")); got != "the original bytes\n" {
		t.Errorf("data not restored: %q", got)
	}
	if _, err := os.Stat(filepath.Join(stack.dataDir, "junk.txt")); !os.IsNotExist(err) {
		t.Error("restore did not erase data written after the snapshot")
	}
	if !stack.running(t) {
		t.Error("restore left the application stopped")
	}
}

// TestE2EUpdateRewritesComposeAndRuns proves the whole update path: snapshot,
// in-place compose rewrite with a .bak, pull, and a container actually running
// the new image.
func TestE2EUpdateRewritesComposeAndRuns(t *testing.T) {
	requireE2E(t)
	stack := newE2EStack(t, "busybox:1.36")
	runner, repo := e2eRestic(t)

	writeFile(t, filepath.Join(stack.dataDir, "keep.txt"), "data\n")
	stack.up(t)
	if got := stack.runningImage(t); got != "busybox:1.36" {
		t.Fatalf("initial image = %q", got)
	}

	job := stack.job(JobUpdate, repo)
	job.Targets = []ServiceTarget{{
		Service:      "keeper",
		CurrentImage: "busybox:1.36",
		TargetImage:  "busybox:1.37",
	}}
	ex := &Executor{Job: job, Restic: runner, Hostname: "e2e",
		Emit: func(step, msg string) { t.Logf("  [%s] %s", step, msg) }}

	snapID, err := ex.Run(context.Background())
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if snapID == "" {
		t.Fatal("update took no snapshot")
	}

	// The compose file was rewritten in place, with the original kept.
	body := readFile(t, stack.compose)
	if !strings.Contains(body, "image: busybox:1.37") {
		t.Errorf("compose not rewritten:\n%s", body)
	}
	if !strings.Contains(body, "# keep it simple") {
		t.Error("rewrite dropped a comment")
	}
	bak := readFile(t, stack.compose+".bak")
	if !strings.Contains(bak, "image: busybox:1.36") {
		t.Errorf(".bak does not hold the original:\n%s", bak)
	}

	if got := stack.runningImage(t); got != "busybox:1.37" {
		t.Errorf("running image = %q, want busybox:1.37", got)
	}

	// The snapshot recorded the version we left, so a restore can undo this.
	snaps, err := runner.Snapshots(context.Background(), ProjectTag(stack.project))
	if err != nil {
		t.Fatal(err)
	}
	if len(snaps) != 1 || snaps[0].Images["keeper"] != "busybox:1.36" {
		t.Fatalf("snapshot did not record the pre-update image: %#v", snaps)
	}

	// --- roll the update back -------------------------------------------------
	restoreJob := stack.job(JobRestore, repo)
	restoreJob.SnapshotID = snaps[0].ID
	ex = &Executor{Job: restoreJob, Restic: runner, Hostname: "e2e",
		Emit: func(step, msg string) { t.Logf("  [%s] %s", step, msg) }}
	if _, err := ex.Run(context.Background()); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if got := readFile(t, stack.compose); !strings.Contains(got, "image: busybox:1.36") {
		t.Errorf("rollback did not restore the compose file:\n%s", got)
	}
	if got := stack.runningImage(t); got != "busybox:1.36" {
		t.Errorf("after rollback image = %q, want busybox:1.36", got)
	}
}

func reflect_DeepEqualStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
