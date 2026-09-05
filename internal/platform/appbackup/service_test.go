package appbackup

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"system-stats/internal/platform/setup"
)

// restic needs read-write access to manage snapshots at all, and given a
// read-only repository it does not fail — it blocks forever waiting for a lock
// it can never take. So writability is proven when the repository is
// configured, by actually writing, not by reading permission bits: a read-only
// bind mount still reports permissive modes.

func TestAssertRepoWritableAcceptsAWritableDirectory(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "restic")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := assertRepoWritable(repo); err != nil {
		t.Fatalf("assertRepoWritable() = %v, want nil", err)
	}
}

// A repository that does not exist yet is judged by its parent — that is where
// restic will create it.
func TestAssertRepoWritableUsesTheParentForAMissingRepository(t *testing.T) {
	base := t.TempDir()
	if err := assertRepoWritable(filepath.Join(base, "not-created-yet")); err != nil {
		t.Fatalf("assertRepoWritable() = %v, want nil", err)
	}
}

func TestAssertRepoWritableRejectsAReadOnlyDirectory(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: mode bits do not restrict writes")
	}
	repo := filepath.Join(t.TempDir(), "restic")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(repo, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(repo, 0o755) })
	err := assertRepoWritable(repo)
	if err == nil {
		t.Fatal("assertRepoWritable() = nil for a read-only directory")
	}
	if !errors.Is(err, ErrRepoReadOnly) {
		t.Fatalf("error %v does not wrap ErrRepoReadOnly", err)
	}
}

func TestAssertRepoWritableRejectsAMissingPath(t *testing.T) {
	err := assertRepoWritable(filepath.Join(t.TempDir(), "no", "such", "tree"))
	if !errors.Is(err, ErrRepoReadOnly) {
		t.Fatalf("error %v does not wrap ErrRepoReadOnly", err)
	}
}

// The hint has to name the actual cause, because "not writable" on its own
// sends the operator hunting for a permissions problem that is really a
// missing bind mount.
func TestWriteProbeErrorPointsAtTheHostMount(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: mode bits do not restrict writes")
	}
	base := t.TempDir()
	repo := filepath.Join(base, "mnt", "backup")
	// Create the tree writable, then close the leaf: MkdirAll with 0555 would
	// make the intermediate directories unwritable too and fail on the way down.
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(repo, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(repo, 0o755) })
	mirror := filepath.Join(base, "hostroot")
	if err := os.MkdirAll(filepath.Join(mirror, repo), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOST_ROOT", mirror)

	err := assertRepoWritable(repo)
	if err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("error = %v, want it to mention the read-only host mount", err)
	}
}

func TestAssertRepoWritableIgnoresRemoteBackends(t *testing.T) {
	for _, url := range []string{"sftp:backup@nas:/volume1/restic", "s3:s3.example.com/bucket"} {
		if err := assertRepoWritable(url); err != nil {
			t.Errorf("assertRepoWritable(%q) = %v, want nil (remote access is proven by opening the repository)", url, err)
		}
	}
}

// The snapshot an operator reaches for is almost always the newest, but restic
// returns them oldest-first.
func TestSnapshotsAreOrderedNewestFirst(t *testing.T) {
	now := time.Now()
	snaps := []Snapshot{
		{ShortID: "old", Time: now.Add(-2 * time.Hour)},
		{ShortID: "new", Time: now},
		{ShortID: "mid", Time: now.Add(-1 * time.Hour)},
	}
	sort.Slice(snaps, func(i, j int) bool { return snaps[i].Time.After(snaps[j].Time) })
	got := []string{snaps[0].ShortID, snaps[1].ShortID, snaps[2].ShortID}
	want := []string{"new", "mid", "old"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
}

// A filesystem repository is named by its HOST path, but inside a container it
// is always reached at the fixed mount. Confusing the two is how a repository
// ends up "configured" and unreachable.
func TestHostPathMapsToTheFixedMountInsideAContainer(t *testing.T) {
	const hostPath = "/opt/node-stats/backups"
	if got := hostToContainerPath(hostPath, true); got != setup.BackupMountPath {
		t.Fatalf("in a container = %q, want %q", got, setup.BackupMountPath)
	}
	if got := hostToContainerPath(hostPath, false); got != hostPath {
		t.Fatalf("native = %q, want the host path unchanged", got)
	}
	if got := hostToContainerPath("", true); got != "" {
		t.Fatalf("empty path = %q, want empty", got)
	}
}

// The default has to land beside the installation: an operator should recognise
// the directory, not have to work out where the container's data actually lives.
func TestSuggestedBackupPath(t *testing.T) {
	t.Run("docker uses the stack directory", func(t *testing.T) {
		t.Setenv("NODE_STATS_STACK_HOST_DIR", "/opt/node-stats")
		if got := SuggestedBackupPath("/app/data", true); got != "/opt/node-stats/backups" {
			t.Fatalf("got %q", got)
		}
	})
	t.Run("native sits beside the data directory", func(t *testing.T) {
		t.Setenv("NODE_STATS_STACK_HOST_DIR", "")
		if got := SuggestedBackupPath("/var/lib/node-stats/data", false); got != "/var/lib/node-stats/backups" {
			t.Fatalf("got %q", got)
		}
	})
	// In a container that was not told its stack directory, every visible path
	// is a container path: suggesting /app/backups would invite the operator to
	// store a location that means nothing on the host.
	t.Run("no suggestion in a container without the stack dir", func(t *testing.T) {
		t.Setenv("NODE_STATS_STACK_HOST_DIR", "")
		if got := SuggestedBackupPath("/app/data", true); got != "" {
			t.Fatalf("got %q, want no suggestion", got)
		}
	})
	t.Run("falls back when nothing is known", func(t *testing.T) {
		t.Setenv("NODE_STATS_STACK_HOST_DIR", "")
		if got := SuggestedBackupPath("", false); got != "/var/lib/node-stats/backups" {
			t.Fatalf("got %q", got)
		}
	})
}

// Backups hold whatever the applications hold — password-manager data, .env
// files, database contents — so an unencrypted repository must be an explicit
// choice, never the consequence of leaving a field blank.
func TestRepositoryEncryptionIsNeverSkippedByAccident(t *testing.T) {
	base := RepoConfig{Backend: BackendLocal, Path: "/opt/node-stats/backups"}

	t.Run("blank password is refused", func(t *testing.T) {
		err := validateRepo(RepoRequest{RepoConfig: base})
		if err == nil || !strings.Contains(err.Error(), "no encryption") {
			t.Fatalf("err = %v, want a refusal pointing at the explicit opt-out", err)
		}
	})
	t.Run("explicit opt-out is accepted", func(t *testing.T) {
		cfg := base
		cfg.NoPassword = true
		if err := validateRepo(RepoRequest{RepoConfig: cfg}); err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
	})
	t.Run("both at once is a contradiction", func(t *testing.T) {
		cfg := base
		cfg.NoPassword = true
		if err := validateRepo(RepoRequest{RepoConfig: cfg, Password: "x"}); err == nil {
			t.Fatal("a password AND no-encryption were both accepted")
		}
	})
	t.Run("password alone is accepted", func(t *testing.T) {
		if err := validateRepo(RepoRequest{RepoConfig: base, Password: "x"}); err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
	})
}

// An unencrypted repository has no key at all: passing an empty RESTIC_PASSWORD
// would make restic prompt and hang instead of opening it.
func TestUnencryptedRepositoryCarriesNoPasswordEnv(t *testing.T) {
	repo := Resolve(RepoConfig{Backend: BackendLocal, Path: "/b", NoPassword: true}, RepoSecrets{})
	if _, ok := repo.Env["RESTIC_PASSWORD"]; ok {
		t.Error("RESTIC_PASSWORD set for an unencrypted repository; restic would prompt")
	}
	if !repo.NoPassword {
		t.Error("NoPassword not carried to the resolved repository")
	}

	enc := Resolve(RepoConfig{Backend: BackendLocal, Path: "/b"}, RepoSecrets{Password: "s3cret"})
	if enc.Env["RESTIC_PASSWORD"] != "s3cret" || enc.NoPassword {
		t.Errorf("encrypted repository resolved wrong: %#v", enc)
	}
}
