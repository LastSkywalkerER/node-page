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

func TestShouldAutoApply(t *testing.T) {
	cases := []struct {
		name              string
		st                Info
		webhookConfigured bool
		lastTarget        string
		want              bool
	}{
		{"no update available", Info{UpdateAvailable: false, Latest: "v1.2.3"}, false, "", false},
		{"controller: fresh target applies", Info{UpdateAvailable: true, Latest: "main@0560cf2"}, false, "", true},
		{"controller: same target skipped (loop guard)", Info{UpdateAvailable: true, Latest: "main@0560cf2"}, false, "main@0560cf2", false},
		{"controller: newer target after a prior apply", Info{UpdateAvailable: true, Latest: "main@aaaaaaa"}, false, "main@0560cf2", true},
		{"empty latest never applies", Info{UpdateAvailable: true, Latest: ""}, false, "", false},
		{"managed without webhook can't apply", Info{UpdateAvailable: true, Latest: "v1.2.3", ManagedExternally: true}, false, "", false},
		{"managed with webhook applies", Info{UpdateAvailable: true, Latest: "v1.2.3", ManagedExternally: true}, true, "", true},
		{"managed with webhook, same target skipped", Info{UpdateAvailable: true, Latest: "v1.2.3", ManagedExternally: true}, true, "v1.2.3", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := shouldAutoApply(c.st, c.webhookConfigured, c.lastTarget); got != c.want {
				t.Fatalf("shouldAutoApply = %v, want %v", got, c.want)
			}
		})
	}
}

func TestNewerAvailable_PrereleaseVsRelease(t *testing.T) {
	cases := []struct {
		name            string
		current, latest string
		want            bool
	}{
		{"beta offered its matching stable release", "0.7.6-beta.19", "v0.7.6", true},
		{"stable not newer than a higher beta", "0.7.6-beta.19", "v0.7.5", false}, // 0.7.5 < 0.7.6-*
		{"beta offered a higher stable", "0.7.6-beta.1", "v0.7.7", true},
		{"release not 'newer' than its own beta", "0.7.6", "v0.7.6-beta.1", false},
		{"plain release no update to same", "0.7.5", "v0.7.5", false},
		{"non-semver current never updates", "main", "v0.7.6", false},
	}
	for _, c := range cases {
		if got := newerAvailable(c.current, c.latest); got != c.want {
			t.Errorf("%s: newerAvailable(%q,%q) = %v, want %v", c.name, c.current, c.latest, got, c.want)
		}
	}
}

func TestIsPrerelease(t *testing.T) {
	for _, c := range []struct {
		v    string
		want bool
	}{
		{"0.7.6-beta.19", true}, {"v0.7.6-beta.1", true}, {"0.7.5", false},
		{"v0.7.5", false}, {"main", false}, {"", false}, {"-beta", false},
	} {
		if got := isPrerelease(c.v); got != c.want {
			t.Errorf("isPrerelease(%q) = %v, want %v", c.v, got, c.want)
		}
	}
}
