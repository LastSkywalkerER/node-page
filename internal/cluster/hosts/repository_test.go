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

// The self-referential case: the node running node-stats is BOTH the local
// agent host (id=1) AND a guest its OWN local Proxmox connector sees. The
// connector upsert carries this node's LOCAL cluster id as origin. It must
// converge onto id=1 (no second row, no MAC collision), attaching topology —
// it must NOT trip the cross-site guard (that guard is for FOREIGN clusters).
func TestUpsertConnectorHostSelfGuestLocalCluster(t *testing.T) {
	repo := newTestRepo(t)
	repo.SetLocalClusterID("dokploy-abc123") // THIS node's own cluster id
	ctx := context.Background()

	// Seed id=1 as UpsertLocalHost would: real machine MAC + UUID, name dokploy.
	const localMAC = "c6:7e:11:22:33:44"
	const localUUID = "UUID-LOCAL"
	r := repo.(*hostRepository)
	if err := r.db.Model(&Host{}).Where("id = ?", LocalCollectorHostID).Updates(map[string]interface{}{
		"name":          "dokploy",
		"mac_address":   localMAC,
		"hardware_uuid": localUUID,
		"source":        SourceAgent,
	}).Error; err != nil {
		t.Fatal(err)
	}

	// The poller links the self-guest to id=1 (by MAC/UUID/name), so it sends
	// id=1's real MAC plus the guest topology, tagged with the LOCAL cluster id.
	info := ConnectorHostInfo{
		HostInfo: HostInfo{
			OriginCluster: "dokploy-abc123", // == local cluster id
			Name:          "dokploy",
			MacAddress:    localMAC,
			HardwareUUID:  localUUID,
		},
		HostType:    "lxc",
		ParentMAC:   "aa:bb:cc:dd:ee:ff",
		ExternalID:  "proxmox:fp/pve/lxc/103",
		GuestStatus: "running",
	}

	// Call twice — the bug hot-looped, so a second identical cycle must also
	// be error-free and must not spawn a row.
	for i := 0; i < 2; i++ {
		got, err := repo.UpsertConnectorHost(ctx, info)
		if err != nil {
			t.Fatalf("cycle %d: upsert err: %v", i, err)
		}
		if got.ID != LocalCollectorHostID {
			t.Fatalf("cycle %d: self-guest landed on id=%d, want id=1", i, got.ID)
		}
		var count int64
		if err := r.db.Model(&Host{}).Count(&count).Error; err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("cycle %d: %d host rows, want exactly 1 (no duplicate)", i, count)
		}
	}

	local, err := repo.GetHostByID(ctx, LocalCollectorHostID)
	if err != nil {
		t.Fatal(err)
	}
	if local.MacAddress != localMAC {
		t.Fatalf("local MAC mutated: %q, want %q", local.MacAddress, localMAC)
	}
	if local.Source != "agent+connector" {
		t.Fatalf("source = %q, want agent+connector", local.Source)
	}
	if local.ExternalID != "proxmox:fp/pve/lxc/103" || local.HostType != "lxc" ||
		local.ParentMAC != "aa:bb:cc:dd:ee:ff" || local.GuestStatus != "running" {
		t.Fatalf("topology not attached to id=1: %+v", local)
	}
}

// Standalone (no Raft / no cluster id): the local connector calls
// UpsertConnectorHost directly with an EMPTY origin. The self-guest must
// converge onto id=1 without tripping the guard or duplicating.
func TestUpsertConnectorHostSelfGuestStandalone(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()

	const localMAC = "c6:7e:11:22:33:44"
	r := repo.(*hostRepository)
	if err := r.db.Model(&Host{}).Where("id = ?", LocalCollectorHostID).Updates(map[string]interface{}{
		"name":        "dokploy",
		"mac_address": localMAC,
		"source":      SourceAgent,
	}).Error; err != nil {
		t.Fatal(err)
	}

	got, err := repo.UpsertConnectorHost(ctx, ConnectorHostInfo{
		HostInfo:    HostInfo{Name: "dokploy", MacAddress: localMAC}, // OriginCluster == ""
		HostType:    "lxc",
		ExternalID:  "proxmox:fp/pve/lxc/103",
		GuestStatus: "running",
	})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if got.ID != LocalCollectorHostID {
		t.Fatalf("self-guest landed on id=%d, want id=1", got.ID)
	}
	var count int64
	r.db.Model(&Host{}).Count(&count)
	if count != 1 {
		t.Fatalf("%d host rows, want exactly 1", count)
	}
	// The agent-row branch persists topology via a column-limited UPDATE and
	// returns the pre-update struct; re-fetch to see the stored row (the poller
	// likewise re-resolves via GetHostByMacAddress after upsert).
	local, err := repo.GetHostByID(ctx, LocalCollectorHostID)
	if err != nil {
		t.Fatal(err)
	}
	if local.Source != "agent+connector" || local.ExternalID != "proxmox:fp/pve/lxc/103" {
		t.Fatalf("topology not attached: %+v", local)
	}
}

// The REMOTE case must still deflect even after the local cluster id is known:
// a connector upsert whose origin is a DIFFERENT cluster and whose MAC collides
// with id=1 (cross-site docker-bridge MAC repeat) must NOT hijack id=1 — it
// gets its own row with a synthetic MAC, and id=1 is left untouched.
func TestUpsertConnectorHostRemoteStillDeflectedWithLocalClusterID(t *testing.T) {
	repo := newTestRepo(t)
	repo.SetLocalClusterID("dokploy-abc123")
	ctx := context.Background()

	got, err := repo.UpsertConnectorHost(ctx, ConnectorHostInfo{
		HostInfo: HostInfo{
			OriginCluster: "office-xyz789", // a DIFFERENT cluster
			Name:          "dokploy",
			MacAddress:    "00:00:00:00:00:01", // == the local collector row's MAC
		},
		HostType:    "vm",
		ParentMAC:   "aa:bb:cc:dd:ee:ff",
		ExternalID:  "proxmox:remote/x/qemu/115",
		GuestStatus: "running",
	})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if got.ID == LocalCollectorHostID {
		t.Fatal("remote connector host hijacked the local collector row")
	}
	if got.MacAddress == "00:00:00:00:00:01" || got.MacAddress == "" {
		t.Fatalf("expected a synthetic MAC, got %q", got.MacAddress)
	}
	local, err := repo.GetHostByID(ctx, LocalCollectorHostID)
	if err != nil {
		t.Fatal(err)
	}
	if local.ExternalID != "" || local.ParentMAC != "" || local.HostType != "" {
		t.Fatalf("local collector row decorated with foreign topology: %+v", local)
	}
}

// UpsertHost's name-match branch (the agent self-replicating its own id=1 row
// while a not-yet-reconciled connector duplicate still holds the real MAC) must
// NOT write the colliding MAC onto the duplicate — that hit the unique index
// (23505) on UPDATE every cycle and wedged the Raft FSM. The deflection keeps
// the duplicate's existing MAC so the upsert succeeds and converges.
func TestUpsertHostNameMatchDeflectsTakenMAC(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()
	r := repo.(*hostRepository)

	const realMAC = "c6:7e:11:22:33:44"
	const uuid = "UUID-LOCAL"
	// id=1 is this machine, holding its real MAC + UUID (as UpsertLocalHost wrote).
	if err := r.db.Model(&Host{}).Where("id = ?", LocalCollectorHostID).Updates(map[string]interface{}{
		"name":          "dokploy",
		"mac_address":   realMAC,
		"hardware_uuid": uuid,
		"source":        SourceAgent,
	}).Error; err != nil {
		t.Fatal(err)
	}
	// A transient connector self-guest duplicate at another id, same name + UUID,
	// but currently carrying a DIFFERENT (synthetic) MAC.
	dup := &Host{
		Name: "dokploy", MacAddress: "b2:de:ad:be:ef:00", HardwareUUID: uuid,
		Source: SourceConnector, ExternalID: "proxmox:fp/pve/lxc/103",
	}
	if err := r.db.Create(dup).Error; err != nil {
		t.Fatal(err)
	}

	// The agent replicates its own id=1 row: same name + UUID, MAC = the real one
	// (already held by id=1). MAC lookup excludes id=1 → misses; name+UUID match
	// the duplicate. Writing realMAC onto the duplicate would collide with id=1.
	got, err := repo.UpsertHost(ctx, HostInfo{
		Name: "dokploy", MacAddress: realMAC, HardwareUUID: uuid, HostID: "",
	})
	if err != nil {
		t.Fatalf("upsert must not 23505-collide on the duplicate: %v", err)
	}
	if got.ID != dup.ID {
		t.Fatalf("name-match landed on id=%d, want the duplicate id=%d", got.ID, dup.ID)
	}
	if got.MacAddress != "b2:de:ad:be:ef:00" {
		t.Fatalf("duplicate MAC was overwritten with the colliding real MAC: %q", got.MacAddress)
	}
	// id=1 keeps its real MAC; no row was lost.
	local, err := repo.GetHostByID(ctx, LocalCollectorHostID)
	if err != nil {
		t.Fatal(err)
	}
	if local.MacAddress != realMAC {
		t.Fatalf("local MAC mutated: %q", local.MacAddress)
	}
	var count int64
	r.db.Model(&Host{}).Count(&count)
	if count != 2 {
		t.Fatalf("%d rows, want 2 (id=1 + duplicate)", count)
	}
}

// UpsertConnectorHost's connector-owned refresh branch must likewise deflect a
// MAC already owned by another row instead of writing it (23505). A pure
// connector-only row keyed by external_id whose incoming MAC now collides with
// a different host keeps its own MAC and updates everything else.
func TestUpsertConnectorHostConnectorOwnedDeflectsTakenMAC(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()
	r := repo.(*hostRepository)

	// Another row owns the MAC the connector upsert will try to claim.
	if err := r.db.Create(&Host{
		Name: "owner", MacAddress: "cc:cc:cc:cc:cc:cc", Source: SourceAgent,
	}).Error; err != nil {
		t.Fatal(err)
	}
	// A pre-existing connector-only row, matched by external_id.
	conn := &Host{
		Name: "guest", MacAddress: "b2:00:00:00:00:01", Source: SourceConnector,
		ExternalID: "proxmox:fp/pve/qemu/200", HostType: "vm",
	}
	if err := r.db.Create(conn).Error; err != nil {
		t.Fatal(err)
	}

	got, err := repo.UpsertConnectorHost(ctx, ConnectorHostInfo{
		HostInfo: HostInfo{
			Name:       "guest",
			MacAddress: "cc:cc:cc:cc:cc:cc", // collides with the "owner" row
		},
		HostType:    "vm",
		ParentMAC:   "aa:bb:cc:dd:ee:ff",
		ExternalID:  "proxmox:fp/pve/qemu/200",
		GuestStatus: "running",
	})
	if err != nil {
		t.Fatalf("connector-owned refresh must not 23505-collide: %v", err)
	}
	if got.ID != conn.ID {
		t.Fatalf("landed on id=%d, want the connector row id=%d", got.ID, conn.ID)
	}
	if got.MacAddress != "b2:00:00:00:00:01" {
		t.Fatalf("connector row MAC overwritten with the colliding MAC: %q", got.MacAddress)
	}
	if got.ParentMAC != "aa:bb:cc:dd:ee:ff" {
		t.Fatalf("topology not refreshed: %+v", got)
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

// cascadeContainer mirrors docker_container_entities' relevant columns: the
// current-state shape (id PK + a host_id column), which the cascade delete
// scopes directly by host_id.
type cascadeContainer struct {
	ID              string    `gorm:"primaryKey;column:id"`
	HostID          *uint     `gorm:"primaryKey;column:host_id;index"`
	MetricTimestamp time.Time `gorm:"column:metric_timestamp"`
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
				HostID:          &hid,
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
