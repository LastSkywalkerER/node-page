package setup

import (
	"fmt"
	"strings"

	"system-stats/internal/app/dockerenv"
)

// RunningInDocker reports whether the process runs inside a container (exported
// wrapper over dockerenv.Running so callers needn't import dockerenv directly).
func RunningInDocker() bool { return dockerenv.Running() }

// RequestRecreate asks the controller to recreate the app container so it
// re-reads the freshly written .env (used by post-setup settings changes that
// only take effect at process start: ports, Prometheus, log level, …).
//
// It returns restartPending=true when a controller-driven recreate was queued
// (Docker, not managed externally). On native or externally-managed
// deployments there is no controller to drive, so it returns
// restartPending=false and the caller surfaces a "restart required" hint.
//
// Unlike the update flow it does NOT set PullBeforeApply — the image is
// unchanged, we only need a recreate to reload the environment. dbType/dbDSN
// describe the CURRENTLY running engine so the regenerated compose keeps the
// same DB topology (mirrors update.updateDocker's reconstruction).
func RequestRecreate(dataDir, dbType, dbDSN string) (restartPending bool, err error) {
	if !dockerenv.Running() || ManagedExternally() {
		return false, nil
	}
	ds, _ := ReadDesiredState(dataDir)
	if ds == nil {
		mode := DBModeSQLite
		dsn := ""
		if strings.EqualFold(dbType, "postgres") {
			mode, dsn = DBModePostgresExternal, dbDSN
		}
		ds = &DesiredState{DBMode: mode, DBDSN: dsn}
	}
	ds.Generation++
	if err := WriteDesiredState(dataDir, *ds); err != nil {
		return false, fmt.Errorf("failed to request a recreate from the controller: %w", err)
	}
	return true, nil
}
