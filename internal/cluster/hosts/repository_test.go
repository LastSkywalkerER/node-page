package hosts

import (
	"context"
	"strings"
	"testing"

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
