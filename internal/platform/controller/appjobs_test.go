package controller

import (
	"reflect"
	"testing"

	"system-stats/internal/platform/appbackup"
	"system-stats/internal/platform/setup"
)

// appJobMounts decides what a privileged helper can see, so it is worth
// pinning: only declared paths, parents absorbing children, and never "/".
func TestAppJobMounts(t *testing.T) {
	job := appbackup.Job{
		Project:      "affine",
		ProjectDir:   "/root/affine",
		ComposeFiles: []string{"/root/affine/docker-compose.yml"},
		Paths: []appbackup.BackupPath{
			{Kind: "bind", Source: "/DATA/AppData/affine/postgres"},
			{Kind: "bind", Source: "/DATA/AppData/affine/storage"},
			// A parent of the two above must swallow them.
			{Kind: "bind", Source: "/DATA/AppData/affine"},
			{Kind: "volume", Name: "v", Source: "/var/lib/docker/volumes/v/_data"},
			// Junk that must not become a mount.
			{Kind: "bind", Source: "/"},
			{Kind: "bind", Source: "relative/path"},
			{Kind: "bind", Source: ""},
		},
	}

	got := appJobMounts(job, "/app/stack/data/docker")
	want := []string{
		"/DATA/AppData/affine",
		"/app/stack/data/docker",
		"/root/affine",
		"/var/lib/docker/volumes/v/_data",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("appJobMounts() =\n  %v\nwant\n  %v", got, want)
	}
}

func TestAppJobMountsDedupesComposeDirAgainstProjectDir(t *testing.T) {
	job := appbackup.Job{
		ProjectDir: "/opt/stacks/n8n",
		ComposeFiles: []string{
			"/opt/stacks/n8n/docker-compose.yml",
			"/opt/stacks/n8n/override.yml",
		},
	}
	got := appJobMounts(job, "/data")
	want := []string{"/data", "/opt/stacks/n8n"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("appJobMounts() = %v, want %v", got, want)
	}
}

// A helper must run the image this node runs, never the published default: a
// pinned or beta node would otherwise execute jobs on a different version of
// node-stats than itself.
func TestAppJobImagePrefersTheControllersOwnImage(t *testing.T) {
	c := &controller{run: &fakeRunner{}}
	t.Setenv("NODE_STATS_IMAGE", "ghcr.io/example/img:beta")
	// fakeRunner.docker returns "", so the self-inspect finds nothing and the
	// environment answers.
	if got := c.appJobImage(); got != "ghcr.io/example/img:beta" {
		t.Fatalf("appJobImage() = %q, want the env override", got)
	}

	// With the daemon answering, its reply wins — that is what this container
	// actually runs.
	c = &controller{run: &fakeRunner{output: "node-stats:local\n"}}
	if got := c.appJobImage(); got != "node-stats:local" {
		t.Fatalf("appJobImage() = %q, want the controller's own image", got)
	}
}

func TestAppJobImageFallsBackToDefault(t *testing.T) {
	c := &controller{run: &fakeRunner{}}
	t.Setenv("NODE_STATS_IMAGE", "")
	if got := c.appJobImage(); got != setup.DefaultImage {
		t.Fatalf("appJobImage() = %q, want the published default", got)
	}
	c.jobImage = "ghcr.io/example/img:v1"
	if got := c.appJobImage(); got != "ghcr.io/example/img:v1" {
		t.Fatalf("appJobImage() = %q, want the cached image", got)
	}
}

// Without a runner (before Run wires one) the lookup must not panic.
func TestAppJobImageSurvivesAMissingRunner(t *testing.T) {
	t.Setenv("NODE_STATS_IMAGE", "")
	if got := (&controller{}).appJobImage(); got != setup.DefaultImage {
		t.Fatalf("appJobImage() = %q", got)
	}
}

// The backup mount lives in the app's stanza, so changing it must recreate the
// container — otherwise the repository stays invisible inside a running app.
func TestBackupPathChangeRecreatesTheApp(t *testing.T) {
	base := setup.DesiredState{DBMode: setup.DBModeSQLite}
	withPath := base
	withPath.BackupHostPath = "/opt/node-stats/backups"
	if appHash(base) == appHash(withPath) {
		t.Fatal("appHash ignores BackupHostPath; the controller would never mount it")
	}
	moved := withPath
	moved.BackupHostPath = "/mnt/backup"
	if appHash(withPath) == appHash(moved) {
		t.Fatal("appHash ignores a change of backup path")
	}
}
