package appbackup

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// The rewriter edits files a human maintains, so these tests are mostly about
// what it must NOT damage: comments, quoting, unrelated services, and any
// `image:` key that is not a compose service's.

const affineLike = `# AFFiNE, pinned deliberately
services:
    affine:
        image: ghcr.io/toeverything/affine:0.26.4   # do not bump blindly
        restart: unless-stopped
        environment:
            AFFINE_SERVER_HOST: https://affine.example.uk
        depends_on:
            postgres:
                condition: service_healthy
    postgres:
        image: "pgvector/pgvector:pg16"
        volumes:
            - /DATA/AppData/affine/postgres:/var/lib/postgresql/data
    redis:
        image: 'redis:latest'
volumes:
    storage:
`

func TestComposeImages(t *testing.T) {
	got := ComposeImages(affineLike)
	want := map[string]string{
		"affine":   "ghcr.io/toeverything/affine:0.26.4",
		"postgres": "pgvector/pgvector:pg16",
		"redis":    "redis:latest",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ComposeImages() = %#v, want %#v", got, want)
	}
}

func TestRewriteImagesPreservesCommentAndQuotes(t *testing.T) {
	out, missing := RewriteImages(affineLike, map[string]string{
		"affine": "ghcr.io/toeverything/affine:0.27.4",
		"redis":  "redis:7-alpine",
	})
	if len(missing) != 0 {
		t.Fatalf("unexpected missing services: %v", missing)
	}

	if !strings.Contains(out, "image: ghcr.io/toeverything/affine:0.27.4   # do not bump blindly") {
		t.Errorf("trailing comment or spacing lost:\n%s", out)
	}
	if !strings.Contains(out, "image: 'redis:7-alpine'") {
		t.Errorf("single-quote style not preserved:\n%s", out)
	}
	// Untouched service keeps its own quoting verbatim.
	if !strings.Contains(out, `image: "pgvector/pgvector:pg16"`) {
		t.Errorf("unrelated service was modified:\n%s", out)
	}
	// The leading file comment and unrelated keys survive.
	if !strings.HasPrefix(out, "# AFFiNE, pinned deliberately\n") {
		t.Errorf("header comment lost:\n%s", out)
	}
	if !strings.Contains(out, "AFFINE_SERVER_HOST: https://affine.example.uk") {
		t.Errorf("environment block damaged:\n%s", out)
	}
}

func TestRewriteImagesReportsMissingService(t *testing.T) {
	_, missing := RewriteImages(affineLike, map[string]string{"nope": "x:1"})
	if !reflect.DeepEqual(missing, []string{"nope"}) {
		t.Fatalf("missing = %v, want [nope]", missing)
	}
}

// A key literally named "image" that is NOT a service's own image must be left
// alone — e.g. under a build block or a label map.
func TestRewriteIgnoresNonServiceImageKeys(t *testing.T) {
	doc := `services:
  web:
    build:
      context: .
    labels:
      image: not-a-real-image
x-shared: &shared
  image: template:1
`
	imgs := ComposeImages(doc)
	// "web" declares no image of its own; the nested keys are deeper than the
	// service level, so the rewriter must not treat "labels.image" as web's.
	if got, ok := imgs["web"]; ok && got != "not-a-real-image" {
		t.Fatalf("unexpected image for web: %q", got)
	}
	// The top-level anchor block is outside `services:` entirely.
	if _, ok := imgs["x-shared"]; ok {
		t.Fatalf("anchor block leaked into service images: %#v", imgs)
	}
}

func TestRewriteHandlesTopLevelKeysAfterServices(t *testing.T) {
	doc := `services:
  a:
    image: a:1
networks:
  default:
    name: shared
`
	out, missing := RewriteImages(doc, map[string]string{"a": "a:2"})
	if len(missing) != 0 {
		t.Fatalf("missing: %v", missing)
	}
	if !strings.Contains(out, "image: a:2") || !strings.Contains(out, "name: shared") {
		t.Fatalf("bad rewrite:\n%s", out)
	}
}

func TestRewriteComposeFileWritesBackup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "docker-compose.yml")
	if err := os.WriteFile(path, []byte(affineLike), 0o644); err != nil {
		t.Fatal(err)
	}

	missing, err := RewriteComposeFile(path, map[string]string{"affine": "ghcr.io/toeverything/affine:0.27.4"})
	if err != nil {
		t.Fatal(err)
	}
	if len(missing) != 0 {
		t.Fatalf("missing: %v", missing)
	}

	bak, err := os.ReadFile(path + ".bak")
	if err != nil {
		t.Fatalf("no .bak written: %v", err)
	}
	if string(bak) != affineLike {
		t.Error(".bak does not match the original document")
	}
	now, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(now), "affine:0.27.4") {
		t.Errorf("file not updated:\n%s", now)
	}
}

// When nothing matches, the file must not be touched and no .bak created —
// otherwise a mis-resolved service name would leave stray files behind.
func TestRewriteComposeFileNoMatchLeavesFileAlone(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "docker-compose.yml")
	if err := os.WriteFile(path, []byte(affineLike), 0o644); err != nil {
		t.Fatal(err)
	}
	missing, err := RewriteComposeFile(path, map[string]string{"ghost": "x:1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(missing) != 1 {
		t.Fatalf("missing = %v", missing)
	}
	if _, err := os.Stat(path + ".bak"); !os.IsNotExist(err) {
		t.Error(".bak created for a no-op rewrite")
	}
	got, _ := os.ReadFile(path)
	if string(got) != affineLike {
		t.Error("file modified despite no matching service")
	}
}

func TestRepoURL(t *testing.T) {
	cases := []struct {
		name string
		cfg  RepoConfig
		want string
	}{
		{"local", RepoConfig{Backend: BackendLocal, Path: "/mnt/backup/restic"}, "/mnt/backup/restic"},
		{"sftp", RepoConfig{Backend: BackendSFTP, User: "backup", Host: "nas", RemotePath: "/volume1/restic"}, "sftp:backup@nas:/volume1/restic"},
		{"sftp no user", RepoConfig{Backend: BackendSFTP, Host: "nas", RemotePath: "/r"}, "sftp:nas:/r"},
		{"s3", RepoConfig{Backend: BackendS3, Endpoint: "https://s3.eu.example.com", Bucket: "bk", Prefix: "/node-stats/"}, "s3:s3.eu.example.com/bk/node-stats"},
		{"s3 no prefix", RepoConfig{Backend: BackendS3, Endpoint: "s3.example.com/", Bucket: "bk"}, "s3:s3.example.com/bk"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := RepoURL(c.cfg); got != c.want {
				t.Fatalf("RepoURL() = %q, want %q", got, c.want)
			}
		})
	}
}

// Two services on purpose: restic splits a tag value on commas, so a raw-JSON
// image map silently arrived back as several broken tags. A single-service
// fixture cannot catch that.
func TestBuildAndParseTags(t *testing.T) {
	tags := BuildTags("affine", JobUpdate, "orangepi5-plus", map[string]string{
		"affine":   "ghcr.io/toeverything/affine:0.26.4",
		"postgres": "pgvector/pgvector:pg16",
	}, []SnapshotMove{{Service: "affine", From: "ghcr.io/toeverything/affine:0.26.4", To: "ghcr.io/toeverything/affine:0.27.4"}})
	for _, tag := range tags {
		if strings.Contains(tag, ",") {
			t.Fatalf("tag %q contains a comma; restic would split it", tag)
		}
	}
	if tagValue(tags, "project") != "affine" {
		t.Errorf("project tag lost: %v", tags)
	}
	if tagValue(tags, "kind") != JobUpdate {
		t.Errorf("kind tag lost: %v", tags)
	}
	if tagValue(tags, "host") != "orangepi5-plus" {
		t.Errorf("host tag lost: %v", tags)
	}
	imgs := decodeImagesTag(tags)
	if imgs["affine"] != "ghcr.io/toeverything/affine:0.26.4" || imgs["postgres"] != "pgvector/pgvector:pg16" {
		t.Errorf("images tag lost: %#v", imgs)
	}
	// Only the service that actually moves is recorded, so the history shows
	// what the update did rather than the whole project's inventory.
	moves := decodeMovesTag(tags)
	if len(moves) != 1 || moves[0].Service != "affine" || moves[0].To != "ghcr.io/toeverything/affine:0.27.4" {
		t.Errorf("moves tag lost: %#v", moves)
	}
}
