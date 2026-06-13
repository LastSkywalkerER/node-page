package docker

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/charmbracelet/log"
)

// countingCollector records how many times the daemon was actually scraped.
type countingCollector struct {
	scrapes atomic.Int64
}

func (c *countingCollector) CollectDockerMetrics(ctx context.Context) (DockerMetric, error) {
	c.scrapes.Add(1)
	return sampleMetric(), nil
}
func (c *countingCollector) IsDockerAvailable(ctx context.Context) bool { return true }
func (c *countingCollector) GetContainerLogs(ctx context.Context, ref ContainerLogRef, tail int) (string, error) {
	return "", nil
}
func (c *countingCollector) TraefikDiscoveryReport(ctx context.Context) TraefikDiscoveryReport {
	return TraefikDiscoveryReport{}
}
func (c *countingCollector) Close() error { return nil }

// noopRepo satisfies DockerRepository with no persistence (only Save is used).
type noopRepo struct{ DockerRepository }

func (noopRepo) SaveCurrentMetric(ctx context.Context, metric DockerMetric, hostId uint) error {
	return nil
}

// The save + live-payload Collect calls within a single tick must share ONE
// daemon scrape (collectCacheTTL), but the next tick must scrape afresh.
func TestCollectCoalescesWithinTick(t *testing.T) {
	col := &countingCollector{}
	svc := NewService(log.New(nil), col, noopRepo{}).(*service)

	base := time.Now()
	svc.now = func() time.Time { return base }

	// First Collect of the tick: real scrape.
	if _, err := svc.Collect(context.Background()); err != nil {
		t.Fatal(err)
	}
	// CollectAndSave (Collect again) within the same instant: cached.
	if err := svc.CollectAndSave(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	// Live/SSE payload path collects once more, still within the tick window.
	svc.now = func() time.Time { return base.Add(200 * time.Millisecond) }
	if _, err := svc.Collect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := col.scrapes.Load(); got != 1 {
		t.Fatalf("expected 1 daemon scrape within the tick, got %d", got)
	}

	// Next tick (past collectCacheTTL): fresh scrape.
	svc.now = func() time.Time { return base.Add(collectCacheTTL + time.Second) }
	if _, err := svc.Collect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := col.scrapes.Load(); got != 2 {
		t.Fatalf("expected a fresh scrape on the next tick, got %d total", got)
	}
}
