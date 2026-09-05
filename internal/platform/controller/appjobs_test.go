package controller

import (
	"reflect"
	"testing"

	"system-stats/internal/platform/appbackup"
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

func TestAppJobImageFallsBackToDefault(t *testing.T) {
	c := &controller{}
	t.Setenv("NODE_STATS_IMAGE", "")
	if got := c.appJobImage(); got == "" {
		t.Fatal("appJobImage() returned empty")
	}
	t.Setenv("NODE_STATS_IMAGE", "ghcr.io/example/img:beta")
	if got := c.appJobImage(); got != "ghcr.io/example/img:beta" {
		t.Fatalf("appJobImage() = %q, want the env override", got)
	}
	c.jobImage = "ghcr.io/example/img:v1"
	if got := c.appJobImage(); got != "ghcr.io/example/img:v1" {
		t.Fatalf("appJobImage() = %q, want the applied desired-state image", got)
	}
}
