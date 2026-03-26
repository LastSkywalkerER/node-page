// Package dockerenv detects whether the process is running inside a container.
package dockerenv

import (
	"os"
	"strings"
)

// Running reports true if the process likely runs inside Docker or a similar OCI runtime.
func Running() bool {
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return true
	}
	data, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		return false
	}
	return strings.Contains(string(data), "docker")
}
