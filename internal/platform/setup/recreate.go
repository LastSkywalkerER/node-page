package setup

import (
	"fmt"
	"strings"

	"system-stats/internal/app/dockerenv"
)

// RunningInDocker reports whether the process runs inside a container (exported
// wrapper over dockerenv.Running so callers needn't import dockerenv directly).
func RunningInDocker() bool { return dockerenv.Running() }

// RequestPortChange records new published host ports on the desired state so the
// controller writes them into the stack .env and recreates (Docker only). Empty
// strings leave a port unchanged. Returns restartPending=false on native /
// managed-externally (the caller persists ADDR/RAFT_BIND_ADDR to .env instead).
func RequestPortChange(dataDir, dbType, dbDSN, httpPort, raftPort string) (restartPending bool, err error) {
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
	if strings.TrimSpace(httpPort) != "" {
		ds.HTTPPort = strings.TrimSpace(httpPort)
	}
	if strings.TrimSpace(raftPort) != "" {
		ds.RaftPort = strings.TrimSpace(raftPort)
	}
	ds.Generation++
	if err := WriteDesiredState(dataDir, *ds); err != nil {
		return false, fmt.Errorf("failed to request a port change from the controller: %w", err)
	}
	return true, nil
}

// RequestGatewayState reconciles the desired-state's Gateway section with want.
// It is idempotent: when the stored section already equals want nothing is
// written (so the gateway materializer can call it every cycle). Returns
// changed=true when a new generation was written. No-op on native /
// managed-externally deployments (no controller). A disabled want on a state
// that never had a gateway section writes nothing either.
func RequestGatewayState(dataDir, dbType, dbDSN string, want GatewayProvision) (changed bool, err error) {
	if !dockerenv.Running() || ManagedExternally() {
		return false, nil
	}
	return ReconcileGatewayDesiredState(dataDir, dbType, dbDSN, want)
}

// ReconcileGatewayDesiredState is RequestGatewayState without the deployment
// gate (exported for the controller's tests).
func ReconcileGatewayDesiredState(dataDir, dbType, dbDSN string, want GatewayProvision) (changed bool, err error) {
	ds, _ := ReadDesiredState(dataDir)
	if ds == nil {
		if !want.Enabled {
			return false, nil
		}
		mode := DBModeSQLite
		dsn := ""
		if strings.EqualFold(dbType, "postgres") {
			mode, dsn = DBModePostgresExternal, dbDSN
		}
		ds = &DesiredState{DBMode: mode, DBDSN: dsn}
	}
	if !want.Enabled {
		if ds.Gateway == nil || !ds.Gateway.Enabled {
			return false, nil
		}
		ds.Gateway = &GatewayProvision{Enabled: false}
	} else {
		if ds.Gateway != nil && ds.Gateway.Equal(want) {
			return false, nil
		}
		gw := want
		ds.Gateway = &gw
	}
	// A gateway-only change must not re-pull the image — unless a pull request
	// is still PENDING (the controller has not executed it yet, e.g. it was
	// blocked or the registry was down): dropping the flag then would silently
	// lose an update the operator asked for.
	if ds.PullBeforeApply {
		if st, err := ReadControllerStatus(dataDir); err == nil && st != nil && st.PullAppliedGeneration >= ds.Generation {
			ds.PullBeforeApply = false
		}
	}
	ds.Generation++
	if err := WriteDesiredState(dataDir, *ds); err != nil {
		return false, fmt.Errorf("failed to request the gateway state from the controller: %w", err)
	}
	return true, nil
}

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
