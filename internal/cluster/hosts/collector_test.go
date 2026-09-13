package hosts

import "testing"

// The pinned NODE_STATS_IPV4 is only overridden when the machine demonstrably
// does not hold that address any more — and notation must never be mistaken for
// a mismatch, or a perfectly good pin would be demoted.
func TestMachineHoldsIP(t *testing.T) {
	local := []string{"192.168.0.103", " 10.8.0.2 "}
	for _, ip := range []string{"192.168.0.103", "10.8.0.2", "::ffff:192.168.0.103"} {
		if !machineHoldsIP(local, ip) {
			t.Errorf("machineHoldsIP(%q) = false, want true", ip)
		}
	}
	for _, ip := range []string{"192.168.0.110", "", "not-an-ip"} {
		if machineHoldsIP(local, ip) {
			t.Errorf("machineHoldsIP(%q) = true, want false", ip)
		}
	}
}
