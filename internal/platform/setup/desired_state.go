package setup

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// File names exchanged between the app and the controller on the shared data
// volume (NODE_STATS_DATA_DIR / the app's /app/data mount).
const (
	DesiredStateFile     = "desired-state.json"
	ControllerStatusFile = "controller-status.json"
)

// Controller status phases written to ControllerStatusFile.
const (
	PhaseIdle     = "idle"
	PhaseApplying = "applying"
	PhaseApplied  = "applied"
	PhaseError    = "error"
	PhaseDisabled = "disabled"
)

// ControllerStatus is the controller→app heartbeat/result, surfaced by the
// wizard so the operator sees "applying…", "applied", or an error.
type ControllerStatus struct {
	Generation int    `json:"generation"`
	Phase      string `json:"phase"`
	Message    string `json:"message,omitempty"`
	Error      string `json:"error,omitempty"`
	UpdatedAt  string `json:"updated_at,omitempty"`
	// Services is the per-unit view (compose, db, app, traefik): each unit is
	// reconciled independently, so one can be in error while the others are
	// applied. The top-level Phase/Message/Error summarise them.
	Services map[string]ServiceStatus `json:"services,omitempty"`
	// PullAppliedGeneration is the desired-state generation whose image pull
	// the app unit last executed — lets writers of gateway-only changes know
	// whether a pending pull may be dropped (RequestGatewayState).
	PullAppliedGeneration int `json:"pull_applied_generation,omitempty"`
}

// ServiceStatus is one unit's reconcile state.
type ServiceStatus struct {
	Phase     string `json:"phase"`
	Message   string `json:"message,omitempty"`
	Error     string `json:"error,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
	// NextRetry / Attempts are set while the unit is failing (back-off).
	NextRetry string `json:"next_retry,omitempty"`
	Attempts  int    `json:"attempts,omitempty"`
}

// ServiceUnitTraefik is the Services key of the managed gateway unit.
const ServiceUnitTraefik = "traefik"

// UnitView returns the status of one unit as a standalone ControllerStatus (the
// shape the UI already renders), or the whole status when the controller
// predates per-unit reporting.
func (st ControllerStatus) UnitView(unit string) ControllerStatus {
	ss, ok := st.Services[unit]
	if !ok {
		return st
	}
	out := ControllerStatus{Generation: st.Generation, Phase: ss.Phase, Message: ss.Message, Error: ss.Error, UpdatedAt: ss.UpdatedAt}
	if ss.Attempts > 0 {
		out.Message = fmt.Sprintf("%s (attempt %d, retrying)", ss.Message, ss.Attempts)
	}
	return out
}

// Hash returns a digest of the descriptor INCLUDING Generation, so each new
// write (which bumps Generation) triggers exactly one controller apply — this
// is what lets "update now" force a pull+recreate even when the topology is
// unchanged. The controller persists the applied hash so a controller restart
// doesn't re-apply an already-applied state.
func (ds DesiredState) Hash() string {
	b, _ := json.Marshal(ds)
	sum := sha256.Sum256(b)
	return fmt.Sprintf("%x", sum)
}

// WriteDesiredState atomically writes the descriptor to dir/desired-state.json.
func WriteDesiredState(dir string, ds DesiredState) error {
	return writeJSONAtomic(filepath.Join(dir, DesiredStateFile), ds, 0o600)
}

// ReadDesiredState loads the descriptor from dir/desired-state.json. It returns
// (nil, nil) when the file does not exist yet (fresh install, pre-wizard).
func ReadDesiredState(dir string) (*DesiredState, error) {
	b, err := os.ReadFile(filepath.Join(dir, DesiredStateFile))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var ds DesiredState
	if err := json.Unmarshal(b, &ds); err != nil {
		return nil, err
	}
	return &ds, nil
}

// WriteControllerStatus atomically writes the controller status to
// dir/controller-status.json.
func WriteControllerStatus(dir string, st ControllerStatus) error {
	return writeJSONAtomic(filepath.Join(dir, ControllerStatusFile), st, 0o644)
}

// ReadControllerStatus loads the controller status; returns (nil, nil) when absent.
func ReadControllerStatus(dir string) (*ControllerStatus, error) {
	b, err := os.ReadFile(filepath.Join(dir, ControllerStatusFile))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var st ControllerStatus
	if err := json.Unmarshal(b, &st); err != nil {
		return nil, err
	}
	return &st, nil
}

// writeJSONAtomic marshals v and writes it via a temp file + rename so readers
// never observe a partially written file.
func writeJSONAtomic(path string, v any, mode os.FileMode) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, mode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
