package appbackup

import (
	"reflect"
	"testing"

	docker "system-stats/internal/metrics/docker"
)

// affineApp mirrors the real shape of the AFFiNE stack on the Orange Pi:
// CasaOS-style bind mounts, a docker socket mount to be ignored, and a nested
// path that must fold into its parent.
func affineApp() *docker.DockerApplication {
	return &docker.DockerApplication{
		Project: "affine",
		Containers: []docker.DockerContainer{
			{
				Name: "/affine_server", Service: "affine",
				Image:              "ghcr.io/toeverything/affine:0.26.4",
				ComposeWorkingDir:  "/root/affine",
				ComposeConfigFiles: "/root/affine/docker-compose.yml",
				UpdateAvailable:    true,
				Mounts: []docker.DockerMount{
					{Type: "bind", Source: "/DATA/AppData/affine/config", Destination: "/root/.affine/config", RW: true},
					{Type: "bind", Source: "/DATA/AppData/affine/storage", Destination: "/root/.affine/storage", RW: true, Size: 496 << 20},
					{Type: "bind", Source: "/etc/localtime", Destination: "/etc/localtime"},
					{Type: "bind", Source: "/var/run/docker.sock", Destination: "/var/run/docker.sock"},
				},
			},
			{
				Name: "/affine_postgres", Service: "postgres",
				Image:              "pgvector/pgvector:pg16",
				ComposeWorkingDir:  "/root/affine",
				ComposeConfigFiles: "/root/affine/docker-compose.yml",
				Mounts: []docker.DockerMount{
					{Type: "bind", Source: "/DATA/AppData/affine/postgres", Destination: "/var/lib/postgresql/data", RW: true},
				},
			},
			{
				Name: "/affine_redis", Service: "redis",
				Image:              "redis:latest",
				ComposeWorkingDir:  "/root/affine",
				ComposeConfigFiles: "/root/affine/docker-compose.yml",
				Mounts: []docker.DockerMount{
					{Type: "volume", Name: "affine_redis_data", Source: "/var/lib/docker/volumes/affine_redis_data/_data", RW: true, Size: 1 << 20},
					{Type: "tmpfs", Source: "/tmp/scratch"},
				},
			},
		},
	}
}

func TestResolvePathsSkipsRuntimeMounts(t *testing.T) {
	got := ResolvePaths(affineApp())

	var sources []string
	for _, p := range got {
		sources = append(sources, p.Source)
	}
	want := []string{
		"/DATA/AppData/affine/config",
		"/DATA/AppData/affine/postgres",
		"/DATA/AppData/affine/storage",
		"/var/lib/docker/volumes/affine_redis_data/_data",
	}
	if !reflect.DeepEqual(sources, want) {
		t.Fatalf("paths =\n  %v\nwant\n  %v", sources, want)
	}
	for _, p := range got {
		if p.Source == "/DATA/AppData/affine/storage" {
			if p.Size != 496<<20 {
				t.Errorf("size lost for storage: %d", p.Size)
			}
			if !reflect.DeepEqual(p.Services, []string{"affine"}) {
				t.Errorf("services = %v, want [affine]", p.Services)
			}
		}
		if p.Source == "/var/lib/docker/volumes/affine_redis_data/_data" && p.Kind != "volume" {
			t.Errorf("volume mount classified as %q", p.Kind)
		}
	}
}

// A parent bind mount must absorb its children: snapshotting both stores the
// data twice, and a restore wiping the parent would erase the child anyway.
func TestResolvePathsFoldsNestedIntoParent(t *testing.T) {
	app := &docker.DockerApplication{
		Containers: []docker.DockerContainer{
			{Name: "/a", Service: "a", Mounts: []docker.DockerMount{
				{Type: "bind", Source: "/DATA/AppData/app", RW: true},
			}},
			{Name: "/b", Service: "b", Mounts: []docker.DockerMount{
				{Type: "bind", Source: "/DATA/AppData/app/db", RW: true},
			}},
		},
	}
	got := ResolvePaths(app)
	if len(got) != 1 || got[0].Source != "/DATA/AppData/app" {
		t.Fatalf("paths = %#v, want only the parent", got)
	}
	if !reflect.DeepEqual(got[0].Services, []string{"a", "b"}) {
		t.Fatalf("nested service not folded into parent: %v", got[0].Services)
	}
}

func TestResolveProject(t *testing.T) {
	dir, files := ResolveProject(affineApp())
	if dir != "/root/affine" {
		t.Errorf("dir = %q, want /root/affine", dir)
	}
	if !reflect.DeepEqual(files, []string{"/root/affine/docker-compose.yml"}) {
		t.Errorf("files = %v", files)
	}
}

func TestResolveProjectFallsBackToComposeFileDir(t *testing.T) {
	app := &docker.DockerApplication{Containers: []docker.DockerContainer{
		{Name: "/x", ComposeConfigFiles: "/opt/stacks/x/compose.yaml"},
	}}
	dir, files := ResolveProject(app)
	if dir != "/opt/stacks/x" || len(files) != 1 {
		t.Fatalf("dir = %q, files = %v", dir, files)
	}
}

func TestResolveServices(t *testing.T) {
	got := ResolveServices(affineApp())
	if len(got) != 3 {
		t.Fatalf("services = %d, want 3", len(got))
	}
	// Sorted by service name: affine, postgres, redis.
	if got[0].Service != "affine" || got[0].Repo != "ghcr.io/toeverything/affine" || got[0].Tag != "0.26.4" {
		t.Errorf("affine = %#v", got[0])
	}
	if !got[0].UpdateAvailable {
		t.Error("update_available not carried through")
	}
	if got[2].Service != "redis" || got[2].Tag != "latest" {
		t.Errorf("redis = %#v", got[2])
	}
}

func TestSplitImageRef(t *testing.T) {
	cases := []struct{ in, repo, tag string }{
		{"ghcr.io/toeverything/affine:0.26.4", "ghcr.io/toeverything/affine", "0.26.4"},
		{"redis", "redis", "latest"},
		{"redis:7-alpine", "redis", "7-alpine"},
		{"registry.local:5000/app", "registry.local:5000/app", "latest"},
		{"registry.local:5000/app:2", "registry.local:5000/app", "2"},
		{"nginx@sha256:abc", "nginx", ""},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			repo, tag := SplitImageRef(c.in)
			if repo != c.repo || tag != c.tag {
				t.Fatalf("SplitImageRef(%q) = (%q, %q), want (%q, %q)", c.in, repo, tag, c.repo, c.tag)
			}
		})
	}
}
