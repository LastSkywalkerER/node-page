package proxmox

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/charmbracelet/log"
	"github.com/glebarez/sqlite"
	gorm2 "gorm.io/gorm"
	"gorm.io/gorm/logger"

	hosts "system-stats/internal/cluster/hosts"
)

// newFreezePoller wires a standalone poller (no Raft) over a real sqlite-backed
// hosts repository, so upsertHost exercises the full freeze → pending-change →
// approve loop end to end.
func newFreezePoller(t *testing.T) (*Poller, hosts.Repository, *gorm2.DB) {
	t.Helper()
	db, err := gorm2.Open(sqlite.Open(":memory:"), &gorm2.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&hosts.Host{}, &hosts.HostPendingChange{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := db.Create(&hosts.Host{ID: hosts.LocalCollectorHostID, Name: "local-node", MacAddress: "00:00:00:00:00:01"}).Error; err != nil {
		t.Fatalf("seed local row: %v", err)
	}
	repo := hosts.NewRepository(db)
	p := NewPoller(PollerDeps{Logger: log.New(io.Discard), HostRepo: repo})
	return p, repo, db
}

func freezeGuestInfo(name string) hosts.ConnectorHostInfo {
	return hosts.ConnectorHostInfo{
		HostInfo: hosts.HostInfo{Name: name, MacAddress: "aa:bb:cc:dd:ee:10", OS: "linux"},
		HostType: hosts.HostTypeVM, ExternalID: "pve:fp/node/qemu/105", GuestStatus: "running",
	}
}

func backdate(t *testing.T, db *gorm2.DB, hostID uint) {
	t.Helper()
	old := time.Now().Add(-time.Hour)
	if err := db.Model(&hosts.Host{}).Where("id = ?", hostID).Update("created_at", old).Error; err != nil {
		t.Fatalf("backdate: %v", err)
	}
}

func pendingCount(t *testing.T, repo hosts.Repository) int {
	t.Helper()
	rows, err := repo.ListPendingChanges(context.Background())
	if err != nil {
		t.Fatalf("list pending: %v", err)
	}
	return len(rows)
}

func TestUpsertHostFreezeParksRenameAndApproveApplies(t *testing.T) {
	p, repo, db := newFreezePoller(t)
	ctx := context.Background()

	// Cycle 1: discovery — row created freely.
	host := p.upsertHost(ctx, freezeGuestInfo("media-vm"))
	if host == nil || host.Name != "media-vm" {
		t.Fatalf("creation failed: %+v", host)
	}
	backdate(t, db, host.ID)

	// Cycle 2: the guest was renamed in PVE — the row must keep its name and a
	// single pending change must be parked.
	host = p.upsertHost(ctx, freezeGuestInfo("renamed-vm"))
	if host == nil || host.Name != "media-vm" {
		t.Fatalf("identity must stay frozen, got %+v", host)
	}
	if n := pendingCount(t, repo); n != 1 {
		t.Fatalf("expected 1 pending change, got %d", n)
	}

	// Cycle 3: same rename again — still exactly one pending row (converged).
	p.upsertHost(ctx, freezeGuestInfo("renamed-vm"))
	if n := pendingCount(t, repo); n != 1 {
		t.Fatalf("repeat polls must converge onto one row, got %d", n)
	}

	// Admin approves.
	changeID := hosts.PendingChangeID(host.MacAddress, "proxmox")
	if err := repo.ApplyPendingChange(ctx, changeID); err != nil {
		t.Fatalf("approve: %v", err)
	}

	// Cycle 4: source and row agree — no new proposal, renamed name persists.
	host = p.upsertHost(ctx, freezeGuestInfo("renamed-vm"))
	if host == nil || host.Name != "renamed-vm" {
		t.Fatalf("approved rename must stick, got %+v", host)
	}
	if n := pendingCount(t, repo); n != 0 {
		t.Fatalf("no proposal expected after approval, got %d", n)
	}
}

func TestUpsertHostFreezeClearsPendingOnRevert(t *testing.T) {
	p, repo, db := newFreezePoller(t)
	ctx := context.Background()

	host := p.upsertHost(ctx, freezeGuestInfo("media-vm"))
	backdate(t, db, host.ID)

	p.upsertHost(ctx, freezeGuestInfo("renamed-vm"))
	if n := pendingCount(t, repo); n != 1 {
		t.Fatalf("expected a parked proposal, got %d", n)
	}

	// The rename was reverted at the source — the proposal must be withdrawn.
	p.upsertHost(ctx, freezeGuestInfo("media-vm"))
	if n := pendingCount(t, repo); n != 0 {
		t.Fatalf("reverted proposal must be cleared, got %d", n)
	}
}

func TestUpsertHostFreezeRejectedStaysParked(t *testing.T) {
	p, repo, db := newFreezePoller(t)
	ctx := context.Background()

	host := p.upsertHost(ctx, freezeGuestInfo("media-vm"))
	backdate(t, db, host.ID)
	p.upsertHost(ctx, freezeGuestInfo("renamed-vm"))

	changeID := hosts.PendingChangeID(host.MacAddress, "proxmox")
	row, err := repo.GetPendingChange(ctx, changeID)
	if err != nil {
		t.Fatalf("get pending: %v", err)
	}
	row.Status = hosts.PendingStatusRejected
	if err := repo.UpsertPendingChange(ctx, row); err != nil {
		t.Fatalf("reject: %v", err)
	}

	// Simulate a poller restart (RAM caches gone): the same rename must NOT
	// resurrect the proposal to pending — the stored fingerprint gates it.
	p2 := NewPoller(PollerDeps{Logger: log.New(io.Discard), HostRepo: repo})
	p2.upsertHost(ctx, freezeGuestInfo("renamed-vm"))
	got, err := repo.GetPendingChange(ctx, changeID)
	if err != nil || got.Status != hosts.PendingStatusRejected {
		t.Fatalf("rejected proposal must stay rejected: %+v (%v)", got, err)
	}

	// A DIFFERENT rename is a new proposal — back to pending.
	p2.upsertHost(ctx, freezeGuestInfo("third-name"))
	got, err = repo.GetPendingChange(ctx, changeID)
	if err != nil || got.Status != hosts.PendingStatusPending {
		t.Fatalf("new value must re-propose as pending: %+v (%v)", got, err)
	}
	if len(got.FieldChanges) != 1 || got.FieldChanges[0].New != "third-name" {
		t.Fatalf("proposal must carry the new value: %+v", got.FieldChanges)
	}
}

func TestUpsertHostBumpsLastSeenWithoutSubmit(t *testing.T) {
	p, repo, db := newFreezePoller(t)
	ctx := context.Background()

	host := p.upsertHost(ctx, freezeGuestInfo("media-vm"))
	if host == nil {
		t.Fatal("creation failed")
	}
	stale := time.Now().Add(-10 * time.Minute)
	if err := db.Model(&hosts.Host{}).Where("id = ?", host.ID).Update("last_seen", stale).Error; err != nil {
		t.Fatalf("stale last_seen: %v", err)
	}

	// Standalone direct upsert refreshes last_seen for a running guest.
	p.upsertHost(ctx, freezeGuestInfo("media-vm"))
	got, err := repo.GetHostByID(ctx, host.ID)
	if err != nil {
		t.Fatalf("get host: %v", err)
	}
	if time.Since(got.LastSeen) > time.Minute {
		t.Fatalf("last_seen must be fresh, got %v", got.LastSeen)
	}
}

func TestResolveExistingRowPrefersExternalID(t *testing.T) {
	p, repo, _ := newFreezePoller(t)
	ctx := context.Background()
	if _, err := repo.UpsertConnectorHost(ctx, freezeGuestInfo("media-vm")); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// Same external id, different MAC (a NIC change): must resolve to the row.
	info := freezeGuestInfo("media-vm")
	info.MacAddress = "aa:bb:cc:dd:ee:99"
	h := p.resolveExistingRow(ctx, info)
	if h == nil || h.ExternalID != info.ExternalID {
		t.Fatalf("must resolve by external id: %+v", h)
	}
	// Unknown external id + unknown MAC → nil (creation).
	info.ExternalID = "pve:fp/node/qemu/999"
	if h := p.resolveExistingRow(ctx, info); h != nil {
		t.Fatalf("unknown guest must resolve to nil, got %+v", h)
	}
}
