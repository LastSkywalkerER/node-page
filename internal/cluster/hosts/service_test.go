package hosts

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/charmbracelet/log"
)

// fakeRepo embeds Repository so only the methods RemoveHost touches are
// implemented; any other call panics (none are expected in these tests).
type fakeRepo struct {
	Repository
	host        *Host
	getErr      error
	cascadedIDs []uint
}

func (f *fakeRepo) GetHostByID(_ context.Context, id uint) (*Host, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	if f.host != nil && f.host.ID == id {
		return f.host, nil
	}
	return nil, errors.New("host not found")
}

func (f *fakeRepo) DeleteHostCascade(_ context.Context, id uint) error {
	f.cascadedIDs = append(f.cascadedIDs, id)
	return nil
}

type fakeReplicator struct {
	enabled     bool
	deletedMACs []string
}

func (f *fakeReplicator) Enabled() bool                                    { return f.enabled }
func (f *fakeReplicator) SubmitHostUpsert(context.Context, HostInfo) error { return nil }
func (f *fakeReplicator) SubmitHostDelete(_ context.Context, m string) error {
	f.deletedMACs = append(f.deletedMACs, m)
	return nil
}

func newTestService(repo Repository, rep RaftReplicator) *service {
	return &service{logger: log.New(io.Discard), hostRepository: repo, raft: rep}
}

func TestRemoveHost_RejectsLocalCollector(t *testing.T) {
	repo := &fakeRepo{}
	s := newTestService(repo, nil)
	err := s.RemoveHost(context.Background(), LocalCollectorHostID)
	if !errors.Is(err, ErrCannotRemoveLocalHost) {
		t.Fatalf("removing id=1 should be rejected, got %v", err)
	}
	if len(repo.cascadedIDs) != 0 {
		t.Fatalf("must not cascade-delete the local collector, deleted %v", repo.cascadedIDs)
	}
}

func TestRemoveHost_ClusterWideByMacWhenRaftEnabled(t *testing.T) {
	repo := &fakeRepo{host: &Host{ID: 5, Name: "peer", MacAddress: "aa:bb:cc:dd:ee:ff"}}
	rep := &fakeReplicator{enabled: true}
	s := newTestService(repo, rep)

	if err := s.RemoveHost(context.Background(), 5); err != nil {
		t.Fatalf("RemoveHost: %v", err)
	}
	if len(rep.deletedMACs) != 1 || rep.deletedMACs[0] != "aa:bb:cc:dd:ee:ff" {
		t.Fatalf("expected replicated delete by MAC, got %v", rep.deletedMACs)
	}
	// The FSM applier performs the local cascade — the service must NOT also
	// cascade directly (that would double-delete and skip peers).
	if len(repo.cascadedIDs) != 0 {
		t.Fatalf("expected no direct local cascade when replicating, got %v", repo.cascadedIDs)
	}
}

func TestRemoveHost_RejectsEmptyMacWhenRaftEnabled(t *testing.T) {
	repo := &fakeRepo{host: &Host{ID: 5, Name: "peer", MacAddress: ""}}
	rep := &fakeReplicator{enabled: true}
	s := newTestService(repo, rep)

	if err := s.RemoveHost(context.Background(), 5); err == nil {
		t.Fatalf("expected error removing a Raft host with no MAC, got nil")
	}
	if len(rep.deletedMACs) != 0 || len(repo.cascadedIDs) != 0 {
		t.Fatalf("must not delete anything when MAC is empty under Raft: macs=%v ids=%v", rep.deletedMACs, repo.cascadedIDs)
	}
}

func TestRemoveHost_LocalCascadeWhenStandalone(t *testing.T) {
	repo := &fakeRepo{host: &Host{ID: 7, Name: "old", MacAddress: "11:22:33:44:55:66"}}
	s := newTestService(repo, nil) // no replicator → standalone

	if err := s.RemoveHost(context.Background(), 7); err != nil {
		t.Fatalf("RemoveHost: %v", err)
	}
	if len(repo.cascadedIDs) != 1 || repo.cascadedIDs[0] != 7 {
		t.Fatalf("expected local cascade of id=7, got %v", repo.cascadedIDs)
	}
}

func TestShouldSubmitUpsert_ThrottlesUnchangedRecords(t *testing.T) {
	s := newTestService(&fakeRepo{}, nil)
	info := HostInfo{Name: "node-a", MacAddress: "aa:bb:cc:dd:ee:ff", IPv4: "10.0.0.5"}
	base := time.Now()

	// First-ever submission always goes through.
	if !s.shouldSubmitUpsert(info, base) {
		t.Fatalf("first submission must be sent")
	}
	// Same record, a tick later (well under the heartbeat) → skip.
	if s.shouldSubmitUpsert(info, base.Add(5*time.Second)) {
		t.Fatalf("unchanged record within heartbeat window must be throttled")
	}
	// Still within window → still skip.
	if s.shouldSubmitUpsert(info, base.Add(hostUpsertHeartbeat-time.Millisecond)) {
		t.Fatalf("unchanged record just before heartbeat must still be throttled")
	}
	// Heartbeat elapsed → liveness refresh is sent even though nothing changed.
	if !s.shouldSubmitUpsert(info, base.Add(hostUpsertHeartbeat)) {
		t.Fatalf("unchanged record must be re-sent once the heartbeat elapses")
	}
}

func TestShouldSubmitUpsert_SendsOnRecordChange(t *testing.T) {
	s := newTestService(&fakeRepo{}, nil)
	info := HostInfo{Name: "node-a", MacAddress: "aa:bb:cc:dd:ee:ff", IPv4: "10.0.0.5"}
	base := time.Now()

	if !s.shouldSubmitUpsert(info, base) {
		t.Fatalf("first submission must be sent")
	}
	// A field change inside the heartbeat window must still be submitted
	// immediately (peers need the new IP without waiting out the throttle).
	changed := info
	changed.IPv4 = "10.0.0.6"
	if !s.shouldSubmitUpsert(changed, base.Add(time.Second)) {
		t.Fatalf("changed record must be sent regardless of the heartbeat window")
	}
	// The changed record is now the baseline → an immediate repeat is throttled.
	if s.shouldSubmitUpsert(changed, base.Add(2*time.Second)) {
		t.Fatalf("repeat of the changed record within the window must be throttled")
	}
}

func TestRemoveHost_LocalCascadeWhenRaftDisabled(t *testing.T) {
	repo := &fakeRepo{host: &Host{ID: 7, Name: "old", MacAddress: "11:22:33:44:55:66"}}
	rep := &fakeReplicator{enabled: false} // wired but disabled
	s := newTestService(repo, rep)

	if err := s.RemoveHost(context.Background(), 7); err != nil {
		t.Fatalf("RemoveHost: %v", err)
	}
	if len(rep.deletedMACs) != 0 {
		t.Fatalf("disabled replicator must not be used, got %v", rep.deletedMACs)
	}
	if len(repo.cascadedIDs) != 1 || repo.cascadedIDs[0] != 7 {
		t.Fatalf("expected local cascade of id=7, got %v", repo.cascadedIDs)
	}
}
