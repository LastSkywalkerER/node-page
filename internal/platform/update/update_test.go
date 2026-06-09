package update

import "testing"

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
