package hosts

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func connectorRow(created time.Time) *Host {
	return &Host{
		ID:         7,
		Name:       "media-vm",
		MacAddress: "aa:bb:cc:dd:ee:01",
		Source:     SourceConnector,
		ExternalID: "pve:fp/node/qemu/105",
		CreatedAt:  created,
	}
}

func connectorInfo(name, mac string) ConnectorHostInfo {
	return ConnectorHostInfo{
		HostInfo: HostInfo{Name: name, MacAddress: mac},
		HostType: HostTypeVM, ExternalID: "pve:fp/node/qemu/105", GuestStatus: "running",
	}
}

func TestFreezeIdentityNoRowCreatesFreely(t *testing.T) {
	info := connectorInfo("media-vm", "aa:bb:cc:dd:ee:01")
	frozen, changes := FreezeIdentity(nil, info, time.Now())
	if len(changes) != 0 || frozen.Name != "media-vm" {
		t.Fatalf("creation must be free: %+v %+v", frozen, changes)
	}
}

func TestFreezeIdentityGraceWindowAppliesFreely(t *testing.T) {
	now := time.Now()
	existing := connectorRow(now.Add(-time.Minute))
	info := connectorInfo("renamed-vm", "aa:bb:cc:dd:ee:02")
	frozen, changes := FreezeIdentity(existing, info, now)
	if len(changes) != 0 {
		t.Fatalf("grace window must not park proposals: %+v", changes)
	}
	if frozen.Name != "renamed-vm" || frozen.MacAddress != "aa:bb:cc:dd:ee:02" {
		t.Fatalf("grace window must apply identity freely: %+v", frozen)
	}
}

func TestFreezeIdentityRenameFrozenPastGrace(t *testing.T) {
	now := time.Now()
	existing := connectorRow(now.Add(-time.Hour))
	info := connectorInfo("renamed-vm", "aa:bb:cc:dd:ee:01")
	frozen, changes := FreezeIdentity(existing, info, now)
	if frozen.Name != "media-vm" {
		t.Fatalf("name must stay frozen, got %q", frozen.Name)
	}
	if len(changes) != 1 || changes[0].Field != "name" || changes[0].New != "renamed-vm" || changes[0].Old != "media-vm" {
		t.Fatalf("expected one name proposal, got %+v", changes)
	}
	// Topology and state keep flowing.
	if frozen.GuestStatus != "running" || frozen.ExternalID != info.ExternalID {
		t.Fatalf("non-identity fields must pass through: %+v", frozen)
	}
}

func TestFreezeIdentityMACChangeFrozenPastGrace(t *testing.T) {
	now := time.Now()
	existing := connectorRow(now.Add(-time.Hour))
	info := connectorInfo("media-vm", "AA:BB:CC:DD:EE:99")
	frozen, changes := FreezeIdentity(existing, info, now)
	if frozen.MacAddress != "aa:bb:cc:dd:ee:01" {
		t.Fatalf("MAC must stay frozen, got %q", frozen.MacAddress)
	}
	if len(changes) != 1 || changes[0].Field != "mac_address" || changes[0].New != "aa:bb:cc:dd:ee:99" {
		t.Fatalf("expected one mac proposal, got %+v", changes)
	}
}

func TestFreezeIdentitySyntheticMACNeverProposed(t *testing.T) {
	// NIC became unreadable → the connector falls back to the external-id
	// placeholder. That is a degraded read, not an identity change: keep the
	// stored MAC silently.
	now := time.Now()
	existing := connectorRow(now.Add(-time.Hour))
	info := connectorInfo("media-vm", "pve:fp/node/qemu/105")
	frozen, changes := FreezeIdentity(existing, info, now)
	if frozen.MacAddress != "aa:bb:cc:dd:ee:01" {
		t.Fatalf("stored MAC must be kept, got %q", frozen.MacAddress)
	}
	if len(changes) != 0 {
		t.Fatalf("synthetic MAC must not park a proposal: %+v", changes)
	}
}

func TestFreezeIdentitySuffixedNameEquivalent(t *testing.T) {
	// The repository's uniqueness suffix must never read as a rename.
	now := time.Now()
	existing := connectorRow(now.Add(-time.Hour))
	existing.Name = "media-vm [node/qemu/105]"
	info := connectorInfo("media-vm", "aa:bb:cc:dd:ee:01")
	_, changes := FreezeIdentity(existing, info, now)
	if len(changes) != 0 {
		t.Fatalf("suffixed name must be treated as equal: %+v", changes)
	}
}

func TestFreezeIdentityAgentRowUntouched(t *testing.T) {
	now := time.Now()
	existing := connectorRow(now.Add(-time.Hour))
	existing.Source = SourceAgentConnector
	info := connectorInfo("renamed-vm", "aa:bb:cc:dd:ee:99")
	frozen, changes := FreezeIdentity(existing, info, now)
	if len(changes) != 0 || frozen.Name != "renamed-vm" {
		t.Fatalf("agent-owned rows are not connector-frozen: %+v %+v", frozen, changes)
	}
}

func TestPendingChangeIDStable(t *testing.T) {
	a := PendingChangeID("AA:BB:CC:DD:EE:01", "proxmox")
	b := PendingChangeID("aa:bb:cc:dd:ee:01", "proxmox")
	if a != b || len(a) != 16 {
		t.Fatalf("change id must be case-insensitive and 16 chars: %q %q", a, b)
	}
	if a == PendingChangeID("aa:bb:cc:dd:ee:01", "pbs") {
		t.Fatal("different sources must produce different ids")
	}
}

func TestPendingChangesFingerprintOrderIndependent(t *testing.T) {
	a := PendingChangesFingerprint([]PendingFieldChange{{Field: "name", New: "x"}, {Field: "mac_address", New: "y"}})
	b := PendingChangesFingerprint([]PendingFieldChange{{Field: "mac_address", New: "y"}, {Field: "name", New: "x"}})
	if a != b {
		t.Fatal("fingerprint must not depend on field order")
	}
	if a == PendingChangesFingerprint([]PendingFieldChange{{Field: "name", New: "z"}}) {
		t.Fatal("different proposals must differ")
	}
}

func TestPendingChangeRepoCRUDAndApply(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()

	// Seed a connector-owned host row.
	host, err := repo.UpsertConnectorHost(ctx, connectorInfo("media-vm", "aa:bb:cc:dd:ee:01"))
	if err != nil {
		t.Fatalf("seed host: %v", err)
	}

	changeID := PendingChangeID(host.MacAddress, "proxmox")
	ch := &HostPendingChange{
		ChangeID:    changeID,
		HostMAC:     host.MacAddress,
		HostName:    host.Name,
		Source:      "proxmox",
		Changes:     `[{"field":"name","old":"media-vm","new":"renamed-vm"}]`,
		Fingerprint: PendingChangesFingerprint([]PendingFieldChange{{Field: "name", New: "renamed-vm"}}),
		Status:      PendingStatusPending,
	}
	if err := repo.UpsertPendingChange(ctx, ch); err != nil {
		t.Fatalf("upsert pending: %v", err)
	}
	// Upsert again with a new fingerprint — must converge onto ONE row.
	ch2 := *ch
	ch2.Changes = `[{"field":"name","old":"media-vm","new":"other-vm"}]`
	ch2.Fingerprint = "fp2"
	if err := repo.UpsertPendingChange(ctx, &ch2); err != nil {
		t.Fatalf("re-upsert pending: %v", err)
	}
	rows, err := repo.ListPendingChanges(ctx)
	if err != nil || len(rows) != 1 {
		t.Fatalf("expected exactly one pending row, got %d (%v)", len(rows), err)
	}
	if rows[0].Fingerprint != "fp2" || len(rows[0].FieldChanges) != 1 || rows[0].FieldChanges[0].New != "other-vm" {
		t.Fatalf("row not updated in place: %+v", rows[0])
	}

	// Apply: the host row gets the new name, the proposal disappears.
	if err := repo.ApplyPendingChange(ctx, changeID); err != nil {
		t.Fatalf("apply: %v", err)
	}
	updated, err := repo.GetHostByID(ctx, host.ID)
	if err != nil || updated.Name != "other-vm" {
		t.Fatalf("host not renamed: %+v (%v)", updated, err)
	}
	if _, err := repo.GetPendingChange(ctx, changeID); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("proposal must be gone after apply, got %v", err)
	}
	// Idempotent re-apply.
	if err := repo.ApplyPendingChange(ctx, changeID); err != nil {
		t.Fatalf("re-apply must be a no-op: %v", err)
	}
}

func TestApplyPendingChangeSkipsTakenMAC(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()

	host, err := repo.UpsertConnectorHost(ctx, connectorInfo("media-vm", "aa:bb:cc:dd:ee:01"))
	if err != nil {
		t.Fatalf("seed host: %v", err)
	}
	other := connectorInfo("other-vm", "aa:bb:cc:dd:ee:02")
	other.ExternalID = "pve:fp/node/qemu/106"
	if _, err := repo.UpsertConnectorHost(ctx, other); err != nil {
		t.Fatalf("seed other: %v", err)
	}

	changeID := PendingChangeID(host.MacAddress, "proxmox")
	if err := repo.UpsertPendingChange(ctx, &HostPendingChange{
		ChangeID: changeID,
		HostMAC:  host.MacAddress,
		Source:   "proxmox",
		Changes:  `[{"field":"mac_address","old":"aa:bb:cc:dd:ee:01","new":"aa:bb:cc:dd:ee:02"}]`,
		Status:   PendingStatusPending,
	}); err != nil {
		t.Fatalf("upsert pending: %v", err)
	}
	if err := repo.ApplyPendingChange(ctx, changeID); err != nil {
		t.Fatalf("apply: %v", err)
	}
	// The MAC is owned by another row — the host keeps its current one; the
	// proposal is still consumed (unique index would hard-error otherwise).
	updated, _ := repo.GetHostByID(ctx, host.ID)
	if updated.MacAddress != "aa:bb:cc:dd:ee:01" {
		t.Fatalf("taken MAC must be skipped, got %q", updated.MacAddress)
	}
	if _, err := repo.GetPendingChange(ctx, changeID); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("proposal must be consumed, got %v", err)
	}
}

func TestApplyPendingChangeHostGoneDropsProposal(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()
	changeID := PendingChangeID("aa:bb:cc:dd:ee:55", "proxmox")
	if err := repo.UpsertPendingChange(ctx, &HostPendingChange{
		ChangeID: changeID,
		HostMAC:  "aa:bb:cc:dd:ee:55",
		Source:   "proxmox",
		Changes:  `[{"field":"name","old":"a","new":"b"}]`,
		Status:   PendingStatusPending,
	}); err != nil {
		t.Fatalf("upsert pending: %v", err)
	}
	if err := repo.ApplyPendingChange(ctx, changeID); err != nil {
		t.Fatalf("apply with missing host must not error: %v", err)
	}
	if _, err := repo.GetPendingChange(ctx, changeID); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("orphan proposal must be dropped, got %v", err)
	}
}

func TestDeleteHostCascadeDropsPendingChanges(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&Host{}, &HostPendingChange{}, &cascadeCPU{}, &cascadeMem{},
		&cascadeDisk{}, &cascadeNet{}, &cascadeDocker{}, &cascadeContainer{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := db.Create(&Host{ID: LocalCollectorHostID, Name: "local-node", MacAddress: "00:00:00:00:00:01"}).Error; err != nil {
		t.Fatalf("seed local row: %v", err)
	}
	repo := NewRepository(db)
	ctx := context.Background()
	host, err := repo.UpsertConnectorHost(ctx, connectorInfo("media-vm", "aa:bb:cc:dd:ee:01"))
	if err != nil {
		t.Fatalf("seed host: %v", err)
	}
	changeID := PendingChangeID(host.MacAddress, "proxmox")
	if err := repo.UpsertPendingChange(ctx, &HostPendingChange{
		ChangeID: changeID, HostMAC: host.MacAddress, Source: "proxmox",
		Changes: `[]`, Status: PendingStatusPending,
	}); err != nil {
		t.Fatalf("upsert pending: %v", err)
	}
	if err := repo.DeleteHostCascade(ctx, host.ID); err != nil {
		t.Fatalf("cascade: %v", err)
	}
	if _, err := repo.GetPendingChange(ctx, changeID); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("cascade must drop the host's proposals, got %v", err)
	}
}

func TestServiceApproveRejectPendingChange(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()
	host, err := repo.UpsertConnectorHost(ctx, connectorInfo("media-vm", "aa:bb:cc:dd:ee:01"))
	if err != nil {
		t.Fatalf("seed host: %v", err)
	}
	changeID := PendingChangeID(host.MacAddress, "proxmox")
	seed := func() {
		if err := repo.UpsertPendingChange(ctx, &HostPendingChange{
			ChangeID: changeID, HostMAC: host.MacAddress, Source: "proxmox",
			Changes: `[{"field":"name","old":"media-vm","new":"renamed-vm"}]`,
			Status:  PendingStatusPending, Fingerprint: "fp",
		}); err != nil {
			t.Fatalf("seed pending: %v", err)
		}
	}

	// Standalone: approve applies directly.
	seed()
	s := newTestService(repo, nil)
	if err := s.ApprovePendingChange(ctx, changeID); err != nil {
		t.Fatalf("approve: %v", err)
	}
	updated, _ := repo.GetHostByID(ctx, host.ID)
	if updated.Name != "renamed-vm" {
		t.Fatalf("approve must rename, got %q", updated.Name)
	}

	// Standalone: reject flips the status in place.
	seed()
	if err := s.RejectPendingChange(ctx, changeID); err != nil {
		t.Fatalf("reject: %v", err)
	}
	row, err := repo.GetPendingChange(ctx, changeID)
	if err != nil || row.Status != PendingStatusRejected || row.Fingerprint != "fp" {
		t.Fatalf("reject must keep the row with status=rejected: %+v (%v)", row, err)
	}

	// Unknown id → not found (handler maps to 404).
	if err := s.ApprovePendingChange(ctx, "nope"); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("unknown id must be ErrRecordNotFound, got %v", err)
	}

	// Raft enabled: approve/reject go through the replicator.
	rep := &fakeReplicator{enabled: true}
	sr := newTestService(repo, rep)
	if err := sr.ApprovePendingChange(ctx, changeID); err != nil {
		t.Fatalf("raft approve: %v", err)
	}
	if len(rep.pendingApplies) != 1 || rep.pendingApplies[0] != changeID {
		t.Fatalf("raft approve must submit an apply command: %+v", rep.pendingApplies)
	}
	if err := sr.RejectPendingChange(ctx, changeID); err != nil {
		t.Fatalf("raft reject: %v", err)
	}
	if len(rep.pendingUpserts) != 1 || rep.pendingUpserts[0].Status != PendingStatusRejected {
		t.Fatalf("raft reject must submit a rejected upsert: %+v", rep.pendingUpserts)
	}
}
