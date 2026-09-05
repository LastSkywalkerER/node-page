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
