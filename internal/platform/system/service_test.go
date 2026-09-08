package system

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/charmbracelet/log"

	cpu "system-stats/internal/metrics/cpu"
	disk "system-stats/internal/metrics/disk"
	docker "system-stats/internal/metrics/docker"
	memory "system-stats/internal/metrics/memory"
	network "system-stats/internal/metrics/network"
)

// countingCPU is a cpu.Service whose Collect blocks briefly and counts calls.
type countingCPU struct {
	cpu.Service
	calls atomic.Int64
}

func (c *countingCPU) Collect(ctx context.Context) (cpu.CPUMetric, error) {
	c.calls.Add(1)
	time.Sleep(50 * time.Millisecond)
	return cpu.CPUMetric{UsagePercent: 42}, nil
}

type failMem struct{ memory.Service }

func (failMem) Collect(ctx context.Context) (memory.MemoryMetric, error) {
	return memory.MemoryMetric{}, context.DeadlineExceeded
}

type okDisk struct{ disk.Service }

func (okDisk) Collect(ctx context.Context) (disk.DiskMetric, error) {
	return disk.DiskMetric{Total: 1}, nil
}

type okNet struct{ network.Service }

func (okNet) Collect(ctx context.Context) (network.NetworkMetric, error) {
	return network.NetworkMetric{}, nil
}

type okDocker struct{ docker.Service }

func (okDocker) Collect(ctx context.Context) (docker.DockerMetric, error) {
	return docker.DockerMetric{DockerAvailable: true}, nil
}

// Concurrent CollectSnapshot calls (ticker + on-demand read) must run ONE
// collection pass and hand the same snapshot to every caller.
func TestCollectSnapshotCoalescesConcurrentCallers(t *testing.T) {
	c := &countingCPU{}
	svc := NewService(log.New(nil), c, failMem{}, okDisk{}, okNet{}, okDocker{})

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			snap, err := svc.CollectSnapshot(context.Background())
			if err != nil {
				t.Errorf("collect: %v", err)
				return
			}
			if snap.CPU == nil || snap.CPU.UsagePercent != 42 {
				t.Errorf("cpu missing from snapshot: %+v", snap.CPU)
			}
			if snap.Memory != nil {
				t.Errorf("failed module must be omitted, got %+v", snap.Memory)
			}
		}()
	}
	wg.Wait()
	if got := c.calls.Load(); got != 1 {
		t.Fatalf("expected one collection pass for 5 concurrent callers, got %d", got)
	}

	// Payload omits failed modules and carries the host id only when set.
	snap, _ := svc.Latest()
	p := snap.Payload(7)
	if _, ok := p["memory"]; ok {
		t.Fatal("payload must omit failed modules")
	}
	if p["collecting_host_id"] != uint(7) {
		t.Fatalf("collecting_host_id = %v", p["collecting_host_id"])
	}
	if _, ok := snap.Payload(0)["collecting_host_id"]; ok {
		t.Fatal("host id 0 must not be emitted")
	}
	if _, ok := p["applications"]; !ok {
		t.Fatal("applications projection must ride with docker")
	}
}

// CollectAllCurrent serves the fresh snapshot instead of scanning again.
func TestCollectAllCurrentServesFreshSnapshot(t *testing.T) {
	c := &countingCPU{}
	svc := NewService(log.New(nil), c, failMem{}, okDisk{}, okNet{}, okDocker{})
	if _, err := svc.CollectSnapshot(context.Background()); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := svc.CollectAllCurrent(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if got := c.calls.Load(); got != 1 {
		t.Fatalf("on-demand reads within the fresh window must not re-collect, got %d passes", got)
	}
}
