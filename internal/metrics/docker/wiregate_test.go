package docker

import (
	"testing"
	"time"
)

func wireMetric(cpuPct float64, status, image string, running int) *DockerMetric {
	return &DockerMetric{
		Stacks: []DockerStack{{
			Name: "app",
			Containers: []DockerContainer{{
				ID:     "c1",
				Name:   "app-web-1",
				Image:  image,
				State:  "running",
				Status: status,
				Stats:  DockerStats{CPUPercent: cpuPct, MemoryUsage: uint64(cpuPct * 1e6)},
				Labels: map[string]string{"com.docker.compose.project": "app"},
			}},
			TotalContainers:   1,
			RunningContainers: running,
		}},
		TotalContainers:   1,
		RunningContainers: running,
		DockerAvailable:   true,
	}
}

func TestWireGateFirstSendAlways(t *testing.T) {
	g := NewWireGate(30 * time.Second)
	if !g.ShouldSend(wireMetric(1, "Up 5 minutes", "nginx:1", 1), time.Now()) {
		t.Fatal("first payload must always send")
	}
}

func TestWireGateStatsOnlyChangesThrottled(t *testing.T) {
	g := NewWireGate(30 * time.Second)
	now := time.Now()
	if !g.ShouldSend(wireMetric(1, "Up 5 minutes", "nginx:1", 1), now) {
		t.Fatal("first payload must send")
	}
	// Only volatile fields moved (CPU%, mem, uptime string) — no send.
	if g.ShouldSend(wireMetric(57, "Up 6 minutes", "nginx:1", 1), now.Add(10*time.Second)) {
		t.Fatal("stats-only change within resync must be gated")
	}
	// Resync elapsed — send even though nothing structural changed.
	if !g.ShouldSend(wireMetric(58, "Up 7 minutes", "nginx:1", 1), now.Add(31*time.Second)) {
		t.Fatal("resync must force a send")
	}
}

func TestWireGateInventoryChangeSendsImmediately(t *testing.T) {
	g := NewWireGate(30 * time.Second)
	now := time.Now()
	g.ShouldSend(wireMetric(1, "Up 5 minutes", "nginx:1", 1), now)
	// Image change (a redeploy) is structural — sends on the very next tick.
	if !g.ShouldSend(wireMetric(1, "Up 5 minutes", "nginx:2", 1), now.Add(10*time.Second)) {
		t.Fatal("image change must send immediately")
	}
	// Running-count change (start/stop) is structural too.
	if !g.ShouldSend(wireMetric(1, "Up 5 minutes", "nginx:2", 0), now.Add(20*time.Second)) {
		t.Fatal("running-count change must send immediately")
	}
}

func TestWireGateNilMetricNeverSends(t *testing.T) {
	g := NewWireGate(30 * time.Second)
	if g.ShouldSend(nil, time.Now()) {
		t.Fatal("nil metric must not send")
	}
}

// InventoryHash must not mutate the metric it hashes — the same struct goes
// on to be marshaled for SSE and the wire.
func TestInventoryHashLeavesMetricIntact(t *testing.T) {
	m := wireMetric(42, "Up 5 minutes", "nginx:1", 1)
	_ = InventoryHash(m)
	c := m.Stacks[0].Containers[0]
	if c.Status != "Up 5 minutes" || c.Stats.CPUPercent != 42 {
		t.Fatalf("metric mutated by hashing: %+v", c)
	}
}
