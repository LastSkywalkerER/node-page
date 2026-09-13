package setup

import (
	"testing"
)

// A machine that moves must be able to correct the address the controller pins
// into the stack .env — compose injects it into the container, where it beats
// both the detected address and the app's own .env.
func TestReconcileIPv4DesiredState(t *testing.T) {
	dir := t.TempDir()

	// No desired state: nothing owns this stack, so there is nothing to update.
	if changed, err := ReconcileIPv4DesiredState(dir, "192.168.0.103"); err != nil || changed {
		t.Fatalf("with no desired state: changed=%v err=%v, want false/nil", changed, err)
	}

	if err := WriteDesiredState(dir, DesiredState{DBMode: DBModeSQLite, Generation: 4}); err != nil {
		t.Fatalf("seed desired state: %v", err)
	}

	changed, err := ReconcileIPv4DesiredState(dir, " 192.168.0.103 ")
	if err != nil || !changed {
		t.Fatalf("first write: changed=%v err=%v, want true/nil", changed, err)
	}
	ds, err := ReadDesiredState(dir)
	if err != nil || ds == nil {
		t.Fatalf("read back: %v", err)
	}
	if ds.IPv4 != "192.168.0.103" {
		t.Fatalf("IPv4 = %q, want 192.168.0.103", ds.IPv4)
	}
	if ds.Generation != 5 {
		t.Fatalf("Generation = %d, want 5 (the controller only re-syncs on a new one)", ds.Generation)
	}

	// Idempotent: the same address must not churn generations, or the
	// controller would reconcile on every re-attach check.
	if changed, err := ReconcileIPv4DesiredState(dir, "192.168.0.103"); err != nil || changed {
		t.Fatalf("repeat write: changed=%v err=%v, want false/nil", changed, err)
	}
	if changed, err := ReconcileIPv4DesiredState(dir, ""); err != nil || changed {
		t.Fatalf("empty address: changed=%v err=%v, want false/nil", changed, err)
	}
}
