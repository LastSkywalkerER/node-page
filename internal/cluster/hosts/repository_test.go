package hosts

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func newTestRepo(t *testing.T) Repository {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&Host{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// Real deployments always own id=1 (the local collector row, written by
	// UpsertLocalHost at boot); without it the first created row would land
	// on the reserved id and trip the local-row guards.
	if err := db.Create(&Host{ID: LocalCollectorHostID, Name: "local-node", MacAddress: "00:00:00:00:00:01"}).Error; err != nil {
		t.Fatalf("seed local row: %v", err)
	}
	return NewRepository(db)
}

// Two DIFFERENT machines sharing a hostname (e.g. "dokploy" on two uplinked
// sites) must stay two rows — the second gets a suffixed name. Before the
// hardening they merged by name and flipped the MAC back and forth.
func TestUpsertHostSameNameDifferentMachinesSplit(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()

	a, err := repo.UpsertHost(ctx, HostInfo{
		Name: "dokploy", MacAddress: "aa:aa:aa:aa:aa:01", HostID: "machine-a", OriginCluster: "home",
	})
	if err != nil {
		t.Fatalf("upsert a: %v", err)
	}
	b, err := repo.UpsertHost(ctx, HostInfo{
		Name: "dokploy", MacAddress: "bb:bb:bb:bb:bb:02", HostID: "machine-b", OriginCluster: "office",
	})
	if err != nil {
		t.Fatalf("upsert b: %v", err)
	}
	if a.ID == b.ID {
		t.Fatal("different machines merged into one row")
	}
	if b.Name == "dokploy" || !strings.HasPrefix(b.Name, "dokploy") {
		t.Errorf("second row name = %q, want suffixed dokploy variant", b.Name)
	}
	again, err := repo.GetHostByMacAddress(ctx, "aa:aa:aa:aa:aa:01")
	if err != nil || again.ID != a.ID || again.Name != "dokploy" {
		t.Errorf("first row corrupted: %+v err=%v", again, err)
	}
	if a2, _ := repo.GetHostByID(ctx, a.ID); a2.OriginCluster != "home" {
		t.Errorf("origin_cluster a = %q", a2.OriginCluster)
	}
}

// The SAME machine re-registering with a changed MAC (docker veth churn) must
// still merge by name when the machine-id corroborates it.
func TestUpsertHostSameMachineNewMACMerges(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()

	a, err := repo.UpsertHost(ctx, HostInfo{
		Name: "media-vm", MacAddress: "aa:aa:aa:aa:aa:01", HostID: "machine-a",
	})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	b, err := repo.UpsertHost(ctx, HostInfo{
		Name: "media-vm", MacAddress: "cc:cc:cc:cc:cc:03", HostID: "machine-a",
	})
	if err != nil {
		t.Fatalf("re-upsert: %v", err)
	}
	if a.ID != b.ID {
		t.Fatalf("same machine split into two rows: %d vs %d", a.ID, b.ID)
	}
	if b.MacAddress != "cc:cc:cc:cc:cc:03" {
		t.Errorf("MAC not updated: %q", b.MacAddress)
	}
}

// HardwareUUID corroboration also merges (VM whose hostname AND MAC changed).
func TestUpsertHostHardwareUUIDMerges(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()

	a, _ := repo.UpsertHost(ctx, HostInfo{
		Name: "vm-a", MacAddress: "aa:aa:aa:aa:aa:01", HardwareUUID: "uuid-1",
	})
	b, err := repo.UpsertHost(ctx, HostInfo{
		Name: "vm-a", MacAddress: "dd:dd:dd:dd:dd:04", HardwareUUID: "uuid-1",
	})
	if err != nil {
		t.Fatalf("re-upsert: %v", err)
	}
	if a.ID != b.ID {
		t.Fatal("uuid-corroborated name match should merge")
	}
}

// A remote site's connector upsert whose MAC collides with THIS node's own
// collector row (docker-bridge MACs repeat across machines) must not claim
// or decorate the local row — it gets its own row with a synthetic MAC.
func TestUpsertConnectorHostRemoteMACCollisionDeflected(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()

	got, err := repo.UpsertConnectorHost(ctx, ConnectorHostInfo{
		HostInfo: HostInfo{
			OriginCluster: "mika-local",
			Name:          "dokploy",
			MacAddress:    "00:00:00:00:00:01", // == the local collector row's MAC
		},
		HostType:    "vm",
		ParentMAC:   "aa:bb:cc:dd:ee:ff",
		ExternalID:  "proxmox/x/qemu/115",
		GuestStatus: "running",
	})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if got.ID == LocalCollectorHostID {
		t.Fatal("remote connector host claimed the local collector row")
	}
	if got.MacAddress == "00:00:00:00:00:01" || got.MacAddress == "" {
		t.Fatalf("expected a synthetic MAC, got %q", got.MacAddress)
	}
	if got.ParentMAC != "aa:bb:cc:dd:ee:ff" || got.ExternalID != "proxmox/x/qemu/115" {
		t.Fatalf("topology not stored: %+v", got)
	}

	local, err := repo.GetHostByID(ctx, LocalCollectorHostID)
	if err != nil {
		t.Fatal(err)
	}
	if local.ExternalID != "" || local.ParentMAC != "" {
		t.Fatalf("local collector row was decorated with foreign topology: %+v", local)
	}
}

// When the collision already happened (pre-guard), the same remote command
// must shed the foreign topology from the local row and converge onto a
// separate row.
func TestUpsertConnectorHostHealsDecoratedLocalRow(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()

	// Pre-corrupt the local row the way the old code did.
	r := repo.(*hostRepository)
	if err := r.db.Model(&Host{}).Where("id = ?", LocalCollectorHostID).Updates(map[string]interface{}{
		"external_id": "proxmox/x/qemu/115",
		"parent_mac":  "aa:bb:cc:dd:ee:ff",
		"host_type":   "vm",
		"source":      "agent+connector",
	}).Error; err != nil {
		t.Fatal(err)
	}

	got, err := repo.UpsertConnectorHost(ctx, ConnectorHostInfo{
		HostInfo: HostInfo{
			OriginCluster: "mika-local",
			Name:          "dokploy",
			MacAddress:    "1a:b1:a0:4c:36:30",
		},
		HostType:    "vm",
		ParentMAC:   "aa:bb:cc:dd:ee:ff",
		ExternalID:  "proxmox/x/qemu/115",
		GuestStatus: "running",
	})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if got.ID == LocalCollectorHostID {
		t.Fatal("still resolving to the local collector row")
	}
	local, err := repo.GetHostByID(ctx, LocalCollectorHostID)
	if err != nil {
		t.Fatal(err)
	}
	if local.ExternalID != "" || local.ParentMAC != "" || local.HostType != "" {
		t.Fatalf("foreign topology not healed: %+v", local)
	}
	if local.Source != SourceAgent {
		t.Fatalf("source not reverted: %q", local.Source)
	}
}

// metricRow mirrors the (host_id, timestamp) composite-PK shape of every
// metric table the cascade drains. Defined inline to avoid importing the
// metrics packages (they import this one — that would be an import cycle).
type metricRow struct {
	HostID    *uint     `gorm:"primaryKey;column:host_id"`
	Timestamp time.Time `gorm:"primaryKey;column:timestamp"`
}

type cascadeCPU metricRow
type cascadeMem metricRow
type cascadeDisk metricRow
type cascadeNet metricRow
type cascadeDocker metricRow

func (cascadeCPU) TableName() string    { return "cpu_metrics" }
func (cascadeMem) TableName() string    { return "memory_metrics" }
func (cascadeDisk) TableName() string   { return "disk_metrics" }
func (cascadeNet) TableName() string    { return "network_metrics" }
func (cascadeDocker) TableName() string { return "docker_metrics" }

// cascadeContainer mirrors docker_container_entities' relevant columns
// (id + metric_timestamp composite PK), keyed off the parent docker_metrics
// timestamp the same way the production entity is.
type cascadeContainer struct {
	ID              string    `gorm:"primaryKey;column:id"`
	MetricTimestamp time.Time `gorm:"primaryKey;column:metric_timestamp"`
}

func (cascadeContainer) TableName() string { return "docker_container_entities" }

// TestDeleteHostCascadeChunked verifies the chunked cascade deletes every
// metric row (including docker_container_entities resolved via docker_metrics
// timestamps) for the target host across more than one chunk, and leaves
// another host's rows and the hosts table entry for that other host intact.
func TestDeleteHostCascadeChunked(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&Host{}, &cascadeCPU{}, &cascadeMem{}, &cascadeDisk{},
		&cascadeNet{}, &cascadeDocker{}, &cascadeContainer{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	repo := NewRepository(db)
	ctx := context.Background()

	var target uint = 10
	var other uint = 11
	if err := db.Create(&Host{ID: target, Name: "target", MacAddress: "aa:00:00:00:00:10"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&Host{ID: other, Name: "other", MacAddress: "aa:00:00:00:00:11"}).Error; err != nil {
		t.Fatal(err)
	}

	// More than one chunk's worth of docker rows so the chunked
	// docker_container_entities delete must loop more than once.
	const rows = cascadeDeleteChunkSize + 250
	// Each host writes at its own wall-clock instants (no overlap), as the live
	// collectors do — the container table is keyed by metric_timestamp only.
	seed := func(hostID uint, n int, base time.Time) {
		hid := hostID
		for i := 0; i < n; i++ {
			ts := base.Add(time.Duration(i) * time.Second)
			if err := db.Create(&cascadeCPU{HostID: &hid, Timestamp: ts}).Error; err != nil {
				t.Fatalf("seed cpu: %v", err)
			}
			if err := db.Create(&cascadeDocker{HostID: &hid, Timestamp: ts}).Error; err != nil {
				t.Fatalf("seed docker: %v", err)
			}
			if err := db.Create(&cascadeContainer{
				ID:              fmt.Sprintf("h%d-c%d", hostID, i),
				MetricTimestamp: ts,
			}).Error; err != nil {
				t.Fatalf("seed container: %v", err)
			}
		}
		// One row in each remaining metric table to prove they're drained too.
		hid2 := hostID
		ts := base.Add(24 * time.Hour)
		_ = db.Create(&cascadeMem{HostID: &hid2, Timestamp: ts}).Error
		_ = db.Create(&cascadeDisk{HostID: &hid2, Timestamp: ts}).Error
		_ = db.Create(&cascadeNet{HostID: &hid2, Timestamp: ts}).Error
	}
	targetBase := time.Now().Truncate(time.Second)
	otherBase := targetBase.Add(365 * 24 * time.Hour) // disjoint instants
	seed(target, rows, targetBase)
	seed(other, 5, otherBase)

	if err := repo.DeleteHostCascade(ctx, target); err != nil {
		t.Fatalf("cascade: %v", err)
	}

	count := func(model interface{}, where string, args ...interface{}) int64 {
		var c int64
		q := db.Model(model)
		if where != "" {
			q = q.Where(where, args...)
		}
		if err := q.Count(&c).Error; err != nil {
			t.Fatalf("count: %v", err)
		}
		return c
	}

	if c := count(&cascadeCPU{}, "host_id = ?", target); c != 0 {
		t.Errorf("target cpu rows left: %d", c)
	}
	if c := count(&cascadeDocker{}, "host_id = ?", target); c != 0 {
		t.Errorf("target docker rows left: %d", c)
	}
	if c := count(&cascadeMem{}, "host_id = ?", target); c != 0 {
		t.Errorf("target memory rows left: %d", c)
	}
	if c := count(&cascadeDisk{}, "host_id = ?", target); c != 0 {
		t.Errorf("target disk rows left: %d", c)
	}
	if c := count(&cascadeNet{}, "host_id = ?", target); c != 0 {
		t.Errorf("target network rows left: %d", c)
	}
	// The target's docker containers are gone (chunked, multi-round delete).
	if c := count(&cascadeContainer{}, "id LIKE ?", "h10-%"); c != 0 {
		t.Errorf("target container rows left: %d", c)
	}
	// The hosts row for the target is gone…
	if c := count(&Host{}, "id = ?", target); c != 0 {
		t.Errorf("target host row left: %d", c)
	}
	// …but the OTHER host and all its rows survive untouched.
	if c := count(&Host{}, "id = ?", other); c != 1 {
		t.Errorf("other host row missing: %d", c)
	}
	if c := count(&cascadeCPU{}, "host_id = ?", other); c != 5 {
		t.Errorf("other cpu rows = %d, want 5", c)
	}
	if c := count(&cascadeContainer{}, "id LIKE ?", "h11-%"); c != 5 {
		t.Errorf("other container rows = %d, want 5", c)
	}
}

func TestGetHostByOriginAndName(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()
	if _, err := repo.UpsertConnectorHost(ctx, ConnectorHostInfo{
		HostInfo:   HostInfo{OriginCluster: "mika-local", Name: "dokploy", MacAddress: "1a:b1:a0:4c:36:30"},
		HostType:   "vm",
		ExternalID: "proxmox/x/qemu/115",
	}); err != nil {
		t.Fatal(err)
	}
	got, err := repo.GetHostByOriginAndName(ctx, "mika-local", "dokploy")
	if err != nil {
		t.Fatal(err)
	}
	if got.MacAddress != "1a:b1:a0:4c:36:30" {
		t.Fatalf("wrong row: %+v", got)
	}
	if _, err := repo.GetHostByOriginAndName(ctx, "", "dokploy"); err == nil {
		t.Fatal("empty origin must not match")
	}
}
