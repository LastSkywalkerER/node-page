package update

import (
	"testing"
	"time"
)

func TestNewerAvailable(t *testing.T) {
	cases := []struct {
		current, latest string
		want            bool
	}{
		{"v1.2.3", "v1.2.4", true},
		{"v1.2.3", "v1.3.0", true},
		{"v1.2.3", "v2.0.0", true},
		{"1.2.3", "1.2.3", false},
		{"v1.2.3", "v1.2.3", false},
		{"v1.2.4", "v1.2.3", false},
		{"v1.2.0", "v1.2.0-rc1", false}, // same numeric → not newer
		{"dev", "v1.2.3", false},        // non-semver current → never claims update
		{"docker", "v9.9.9", false},
		{"v1.2.3", "", false},  // no latest yet
		{"v1.0", "v1.1", true}, // 2-part versions
	}
	for _, c := range cases {
		if got := newerAvailable(c.current, c.latest); got != c.want {
			t.Errorf("newerAvailable(%q,%q)=%v, want %v", c.current, c.latest, got, c.want)
		}
	}
}

func TestLookupSum(t *testing.T) {
	body := "abc123  node-stats_v1.2.3_linux_amd64.tar.gz\ndef456  node-stats_v1.2.3_darwin_arm64.tar.gz\n"
	if got := lookupSum(body, "node-stats_v1.2.3_darwin_arm64.tar.gz"); got != "def456" {
		t.Errorf("lookupSum = %q, want def456", got)
	}
	if got := lookupSum(body, "missing.tar.gz"); got != "" {
		t.Errorf("lookupSum(missing) = %q, want empty", got)
	}
}

func TestDateBasedUpdateAvailable(t *testing.T) {
	base := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name                        string
		deployment, current, latest string
		built, published            time.Time
		want                        bool
	}{
		// Source-built docker image (dokploy): release published a day after the build → update.
		{"docker non-semver, newer release", "docker", "docker", "v0.2.1", base, base.Add(24 * time.Hour), true},
		// Release published only minutes after the build (same batch: tag follows main push) → no noise.
		{"docker non-semver, same-batch release", "docker", "docker", "v0.2.1", base, base.Add(30 * time.Minute), false},
		// Build NEWER than the release (operator redeployed after the tag) → up to date.
		{"docker non-semver, build after release", "docker", "main", "v0.2.1", base.Add(time.Hour), base, false},
		// Semver current → the semver compare is authoritative; date check must stay out.
		{"semver current stays on semver path", "docker", "v0.2.1", "v0.2.1", base, base.Add(24 * time.Hour), false},
		// Native dev build (go run / air) → never nag.
		{"native dev build", "native", "dev", "v0.2.1", base, base.Add(24 * time.Hour), false},
		// Unknown build time → cannot decide.
		{"no build time", "docker", "docker", "v0.2.1", time.Time{}, base, false},
		// Latest not a semver → cannot decide.
		{"non-semver latest", "docker", "docker", "", base, base.Add(24 * time.Hour), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := dateBasedUpdateAvailable(tc.deployment, tc.current, tc.latest, tc.built, tc.published)
			if got != tc.want {
				t.Fatalf("dateBasedUpdateAvailable(%s, %q, %q) = %v, want %v", tc.deployment, tc.current, tc.latest, got, tc.want)
			}
		})
	}
}

func TestBuildTime_FallsBackToExecutableMtime(t *testing.T) {
	// Unparseable ldflags date (the source-built image case) → executable mtime.
	got := buildTime("unknown")
	if got.IsZero() {
		t.Fatal("expected a non-zero build time from the executable mtime fallback")
	}
	// A parseable ldflags date wins.
	want := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)
	if got := buildTime("2026-06-01T10:00:00Z"); !got.Equal(want) {
		t.Fatalf("buildTime(ldflags) = %v, want %v", got, want)
	}
}
