package setup

import (
	"os"
	"strings"
)

// ManagedExternally reports whether this deployment's lifecycle is owned by an
// external orchestrator (Dokploy / a Traefik file-provider setup), in which
// case node-stats must NOT mutate or recreate the compose stack itself — doing
// so would fight the orchestrator. Both the setup handler and the controller
// consult this so they agree.
//
// Signals (any one is sufficient):
//   - NODE_STATS_MANAGED_EXTERNALLY=true  (explicit opt-out; recommended)
//   - TRAEFIK_DYNAMIC_DIR set             (Traefik file-provider deployment)
//   - a Dokploy install path is present   (/etc/dokploy)
func ManagedExternally() bool {
	if b := strings.ToLower(strings.TrimSpace(os.Getenv("NODE_STATS_MANAGED_EXTERNALLY"))); b == "true" || b == "1" || b == "yes" {
		return true
	}
	if strings.TrimSpace(os.Getenv("TRAEFIK_DYNAMIC_DIR")) != "" {
		return true
	}
	if fi, err := os.Stat("/etc/dokploy"); err == nil && fi.IsDir() {
		return true
	}
	return false
}
