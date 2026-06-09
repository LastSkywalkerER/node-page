package hosts

import (
	"context"
	"errors"
	"io"
	"testing"

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
