package docker

import "testing"

func TestStripImageDigest(t *testing.T) {
	cases := map[string]string{
		"makeplane/plane-backend:stable@sha256:abc": "makeplane/plane-backend:stable",
		"postgres:18":          "postgres:18",
		"repo@sha256:deadbeef": "repo",
		"sha256:deadbeef":      "sha256:deadbeef", // bare digest has no '@'
		"":                     "",
	}
	for in, want := range cases {
		if got := stripImageDigest(in); got != want {
			t.Errorf("stripImageDigest(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIsBareDigestRef(t *testing.T) {
	bare := []string{"", "sha256:abc123", "<none>:<none>", "<none>@sha256:x"}
	for _, r := range bare {
		if !isBareDigestRef(r) {
			t.Errorf("isBareDigestRef(%q) = false, want true", r)
		}
	}
	real := []string{"postgres:18", "makeplane/plane-backend:stable", "ghcr.io/owner/app:v1"}
	for _, r := range real {
		if isBareDigestRef(r) {
			t.Errorf("isBareDigestRef(%q) = true, want false", r)
		}
	}
}

func TestIsTrackableRef(t *testing.T) {
	trackable := []string{
		"postgres:18",
		"redis:7-alpine",
		"makeplane/plane-backend:stable",
		"ghcr.io/lastskywalkerer/node-page:v1",
		"localhost:5000/app:1",
		"postgres", // no tag → defaults to :latest, still trackable
	}
	for _, r := range trackable {
		if !isTrackableRef(r) {
			t.Errorf("isTrackableRef(%q) = false, want true", r)
		}
	}
	notTrackable := []string{"", "sha256:abc", "<none>:<none>"}
	for _, r := range notTrackable {
		if isTrackableRef(r) {
			t.Errorf("isTrackableRef(%q) = true, want false", r)
		}
	}
}

func TestResolveCheckRef(t *testing.T) {
	tests := []struct {
		name         string
		repoTags     []string
		configImage  string
		summaryImage string
		want         string
	}{
		{
			name:         "repo tag preferred over digest summary (swarm/dokploy)",
			repoTags:     []string{"makeplane/plane-backend:stable"},
			summaryImage: "sha256:b2184f87914802",
			want:         "makeplane/plane-backend:stable",
		},
		{
			name:        "falls back to configured image when no usable repo tag",
			repoTags:    nil,
			configImage: "postgres:18@sha256:deadbeef",
			want:        "postgres:18",
		},
		{
			name:         "skips dangling <none> tag, uses summary",
			repoTags:     []string{"<none>:<none>"},
			summaryImage: "minio/minio:latest",
			want:         "minio/minio:latest",
		},
		{
			name:         "bare digest everywhere → not trackable (locally built)",
			repoTags:     nil,
			configImage:  "sha256:aaa",
			summaryImage: "sha256:bbb",
			want:         "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveCheckRef(tt.repoTags, tt.configImage, tt.summaryImage); got != tt.want {
				t.Errorf("resolveCheckRef(%v, %q, %q) = %q, want %q",
					tt.repoTags, tt.configImage, tt.summaryImage, got, tt.want)
			}
		})
	}
}

func TestLocalRepoDigest(t *testing.T) {
	tests := []struct {
		name        string
		repoDigests []string
		ref         string
		want        string
	}{
		{
			name:        "matches repo, ignores tag",
			repoDigests: []string{"makeplane/plane-backend@sha256:LOCAL"},
			ref:         "makeplane/plane-backend:stable",
			want:        "sha256:LOCAL",
		},
		{
			name:        "picks the matching repo among several",
			repoDigests: []string{"other/repo@sha256:X", "postgres@sha256:Y"},
			ref:         "postgres:18",
			want:        "sha256:Y",
		},
		{
			name:        "falls back to first digest when no repo matches",
			repoDigests: []string{"foo/bar@sha256:Z"},
			ref:         "postgres:18",
			want:        "sha256:Z",
		},
		{
			name:        "no digests → empty",
			repoDigests: nil,
			ref:         "postgres:18",
			want:        "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := localRepoDigest(tt.repoDigests, tt.ref); got != tt.want {
				t.Errorf("localRepoDigest(%v, %q) = %q, want %q", tt.repoDigests, tt.ref, got, tt.want)
			}
		})
	}
}

func TestBuildImageCheckTargets_DedupesByImageID(t *testing.T) {
	stacks := []DockerStack{{Containers: []DockerContainer{
		{ID: "a", ImageID: "sha256:img1", ConfigImage: "makeplane/plane-backend:stable", Image: "sha256:img1"},
		{ID: "b", ImageID: "sha256:img1", ConfigImage: "makeplane/plane-backend:stable", Image: "sha256:img1"}, // same image (api+worker share it)
		{ID: "c", ImageID: "sha256:img2", ConfigImage: "postgres:17-alpine", Image: "postgres:17-alpine"},
		{ID: "d", ImageID: "", Image: "skip-me"}, // no image id → skipped
	}}}
	got := buildImageCheckTargets(stacks)
	if len(got) != 2 {
		t.Fatalf("got %d targets, want 2 (deduped by image ID, empty skipped): %+v", len(got), got)
	}
	byID := map[string]imageCheckTarget{}
	for _, tg := range got {
		byID[tg.imageID] = tg
	}
	if byID["sha256:img1"].configImage != "makeplane/plane-backend:stable" {
		t.Errorf("img1 target lost its config image hint: %+v", byID["sha256:img1"])
	}
	if _, ok := byID["sha256:img2"]; !ok {
		t.Errorf("missing img2 target")
	}
}
