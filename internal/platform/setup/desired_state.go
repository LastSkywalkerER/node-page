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
