package docker

import "testing"

func TestParseTagVersion(t *testing.T) {
	ok := []struct {
		tag                  string
		maj, min, pat, comps int
		variant              string
	}{
		{"v1.2.1", 1, 2, 1, 3, ""},
		{"1.2.1", 1, 2, 1, 3, ""},
		{"7.2.11-alpine", 7, 2, 11, 3, "-alpine"},
		{"3.13.6-management-alpine", 3, 13, 6, 3, "-management-alpine"},
		{"18", 18, 0, 0, 1, ""},
		{"18-alpine", 18, 0, 0, 1, "-alpine"},
		{"1.25", 1, 25, 0, 2, ""},
	}
	for _, c := range ok {
		v, parsed := parseTagVersion(c.tag)
		if !parsed {
			t.Errorf("parseTagVersion(%q) failed, want ok", c.tag)
			continue
		}
		if v.major != c.maj || v.minor != c.min || v.patch != c.pat || v.comps != c.comps || v.variant != c.variant {
			t.Errorf("parseTagVersion(%q) = %+v, want maj=%d min=%d pat=%d comps=%d variant=%q",
				c.tag, v, c.maj, c.min, c.pat, c.comps, c.variant)
		}
	}

	bad := []string{"latest", "stable", "edge", "", "1.2.3rc1", "1.2.3.4", "main", "sha-abc123"}
	for _, tag := range bad {
		if _, parsed := parseTagVersion(tag); parsed {
			t.Errorf("parseTagVersion(%q) = ok, want not-a-version", tag)
		}
	}
}

func TestNextLinkURL(t *testing.T) {
	cases := []struct {
		name, link, host, want string
	}{
		{"single next relative", `</v2/repo/tags/list?last=x&n=100>; rel="next"`, "registry-1.docker.io",
			"https://registry-1.docker.io/v2/repo/tags/list?last=x&n=100"},
		{"prev before next — must pick next", `</v2/repo/tags/list?last=a>; rel="prev", </v2/repo/tags/list?last=z>; rel="next"`, "ghcr.io",
			"https://ghcr.io/v2/repo/tags/list?last=z"},
		{"absolute next", `<https://r.example.com/v2/x/tags/list?last=y>; rel="next"`, "ignored",
			"https://r.example.com/v2/x/tags/list?last=y"},
		{"no next (only prev)", `</v2/repo/tags/list?last=a>; rel="prev"`, "ghcr.io", ""},
		{"empty header", "", "ghcr.io", ""},
		{"malformed", `garbage; rel="next"`, "ghcr.io", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := nextLinkURL(c.link, c.host); got != c.want {
				t.Errorf("nextLinkURL(%q) = %q, want %q", c.link, got, c.want)
			}
		})
	}
}

func TestNewerVersions(t *testing.T) {
	tests := []struct {
		name          string
		current       string
		tags          []string
		wantSameMajor string
		wantHigher    string
	}{
		{
			name:          "pinned semver finds newest minor and newest major",
			current:       "v1.2.1",
			tags:          []string{"v1.2.0", "v1.2.5", "v1.3.2", "v2.0.0", "v2.1.0", "latest", "v1.2.1"},
			wantSameMajor: "v1.3.2",
			wantHigher:    "v2.1.0",
		},
		{
			name:          "variant is respected (only -alpine compared)",
			current:       "7.2.11-alpine",
			tags:          []string{"7.2.12-alpine", "7.4.0-alpine", "8.0.0-alpine", "7.4.0", "8.0.0", "9.0.0-bookworm"},
			wantSameMajor: "7.4.0-alpine",
			wantHigher:    "8.0.0-alpine",
		},
		{
			name:          "bare rolling major tag suppresses same-major, still reports major bump",
			current:       "18",
			tags:          []string{"16", "17", "18", "18.1", "19", "19.2"},
			wantSameMajor: "", // comps<2 → don't suggest 18.1 (digest check covers it)
			wantHigher:    "19.2",
		},
		{
			name:          "already newest → nothing",
			current:       "v2.1.0",
			tags:          []string{"v1.0.0", "v2.0.0", "v2.1.0"},
			wantSameMajor: "",
			wantHigher:    "",
		},
		{
			name:          "non-version current tag → nothing",
			current:       "latest",
			tags:          []string{"1.0.0", "2.0.0"},
			wantSameMajor: "",
			wantHigher:    "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotSame, gotHigher := newerVersions(tt.current, tt.tags)
			if gotSame != tt.wantSameMajor || gotHigher != tt.wantHigher {
				t.Errorf("newerVersions(%q) = (%q, %q), want (%q, %q)",
					tt.current, gotSame, gotHigher, tt.wantSameMajor, tt.wantHigher)
			}
		})
	}
}
