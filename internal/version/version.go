// Package version exposes build metadata injected at link time via -ldflags
// and the deployment shape (docker vs native). It is the single source of
// truth for the public GET /api/v1/version endpoint and the auto-updater.
package version

import "system-stats/internal/app/dockerenv"

// These are overridden at build time with
//
//	-ldflags "-X system-stats/internal/version.Version=v1.2.3 ..."
//
// The defaults make `go build`/air (no ldflags) report a sane dev value.
var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)

// Info is the JSON shape returned by GET /api/v1/version. The auto-update
// fields (latest / update_available / ...) are layered on top in the
// platform/update package (Block 4); this struct carries the build identity.
type Info struct {
	Current    string `json:"current"`            // build version (ldflags), e.g. "v1.2.3" or "dev"
	Commit     string `json:"commit,omitempty"`   // short git SHA (ldflags)
	BuiltAt    string `json:"built_at,omitempty"` // RFC3339 build timestamp (ldflags)
	Deployment string `json:"deployment"`         // "docker" | "native"
}

// Deployment reports how this process is running so the updater and wizard
// can pick the right code path (controller-driven recreate vs self-replace).
func Deployment() string {
	if dockerenv.Running() {
		return "docker"
	}
	return "native"
}

// Get returns the current build identity.
func Get() Info {
	return Info{
		Current:    Version,
		Commit:     Commit,
		BuiltAt:    Date,
		Deployment: Deployment(),
	}
}
