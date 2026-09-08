package network

import (
	"testing"
	"time"
)

// A counter that goes backwards (interface re-created) must re-baseline, not
// wrap the unsigned subtraction into a 10^16 kbps spike.
func TestSpeedCounterRollbackReBaselines(t *testing.T) {
	c := NewNetworkSpeedCalculator()
	c.BeginCalculationBatch()
	c.CalculateSpeed("docker0", 1000, 5000, 10, 50)
	c.EndCalculationBatch()

	// Second batch well past speedMinInterval with a LOWER recv counter.
	c.lastTimestamp = c.lastTimestamp.Add(-10 * time.Second)
	c.BeginCalculationBatch()
	sp := c.CalculateSpeed("docker0", 1200, 4000, 12, 40)
	c.EndCalculationBatch()
	if sp.SpeedKbpsRecv != 0 || sp.SpeedKbpsSent != 0 {
		t.Fatalf("rollback must report zero speeds, got %+v", sp)
	}

	// Third batch: normal delta against the re-based sample.
	c.lastTimestamp = c.lastTimestamp.Add(-10 * time.Second)
	c.BeginCalculationBatch()
	sp = c.CalculateSpeed("docker0", 1200+1250, 4000+2500, 13, 41)
	c.EndCalculationBatch()
	if sp.SpeedKbpsSent < 0.9 || sp.SpeedKbpsSent > 1.1 || sp.SpeedKbpsRecv < 1.9 || sp.SpeedKbpsRecv > 2.1 {
		t.Fatalf("expected ~1 kbps sent / ~2 kbps recv over 10s, got %+v", sp)
	}
}
