package health

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/charmbracelet/log"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	hosts "system-stats/internal/cluster/hosts"
)

func newHealthService(t *testing.T, startedAgo time.Duration) (Service, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&hosts.Host{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	svc := NewService(log.New(io.Discard), hosts.NewRepository(db), time.Now().Add(-startedAgo))
	return svc, db
}

// A host whose boot time is unknown must report NO uptime — not this server's
// process uptime, which showed the identical number on every remote machine
// whenever a backfill had zeroed their boot_time.
func TestGetHealthHostUptimeComesFromBootTimeOnly(t *testing.T) {
	svc, db := newHealthService(t, 10*time.Minute)
	ctx := context.Background()
	now := time.Now()

	known := hosts.Host{ID: 2, Name: "known", MacAddress: "aa:aa:aa:aa:aa:02", BootTime: now.Add(-3 * time.Hour).Unix(), LastSeen: now}
	unknown := hosts.Host{ID: 3, Name: "unknown", MacAddress: "aa:aa:aa:aa:aa:03", BootTime: 0, LastSeen: now}
	for _, h := range []hosts.Host{known, unknown} {
		if err := db.Create(&h).Error; err != nil {
			t.Fatalf("seed %s: %v", h.Name, err)
		}
	}

	id := known.ID
	resp, err := svc.GetHealth(ctx, &id)
	if err != nil {
		t.Fatalf("known host: %v", err)
	}
	if resp.Status != "online" {
		t.Fatalf("known host status = %q, want online", resp.Status)
	}
	if resp.Uptime != "3h" {
		t.Errorf("known host uptime = %q, want 3h", resp.Uptime)
	}
	if resp.HostUptime < 3*3600-2 || resp.HostUptime > 3*3600+2 {
		t.Errorf("known host uptime seconds = %d, want ≈%d", resp.HostUptime, 3*3600)
	}

	id = unknown.ID
	resp, err = svc.GetHealth(ctx, &id)
	if err != nil {
		t.Fatalf("unknown host: %v", err)
	}
	if resp.Status != "online" {
		t.Fatalf("unknown host status = %q, want online (liveness is independent of boot time)", resp.Status)
	}
	if resp.Uptime != "" {
		t.Errorf("unknown-boot host uptime = %q, want empty (never the server's own %q)", resp.Uptime, "10m")
	}
	if resp.HostUptime != 0 {
		t.Errorf("unknown-boot host uptime seconds = %d, want 0", resp.HostUptime)
	}
}

// The host-less /health still reports this process's own uptime.
func TestGetHealthWithoutHostReportsServerUptime(t *testing.T) {
	svc, _ := newHealthService(t, 10*time.Minute)
	resp, err := svc.GetHealth(context.Background(), nil)
	if err != nil {
		t.Fatalf("health: %v", err)
	}
	if resp.Status != "ok" || resp.Uptime != "10m" {
		t.Fatalf("got status=%q uptime=%q, want ok / 10m", resp.Status, resp.Uptime)
	}
}
