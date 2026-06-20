package raft

import (
	"context"
	"testing"
	"time"
)

func TestDecideMembership(t *testing.T) {
	const promoteAfter = 20 * time.Second
	const demoteAfter = 90 * time.Second

	tests := []struct {
		name        string
		peers       []peerHealth
		voters      int
		hasQuorum   bool
		wantPromote []string
		wantDemote  []string
	}{
		{
			name: "promote a non-voter reachable long enough",
			peers: []peerHealth{
				{ID: "b", Suffrage: "nonvoter", ReachableFor: promoteAfter},
			},
			voters:      1,
			hasQuorum:   true,
			wantPromote: []string{"b"},
		},
		{
			name: "do not promote a non-voter not reachable long enough",
			peers: []peerHealth{
				{ID: "b", Suffrage: "nonvoter", ReachableFor: promoteAfter - time.Second},
			},
			voters:    1,
			hasQuorum: true,
		},
		{
			name: "do not promote an unreachable non-voter",
			peers: []peerHealth{
				{ID: "b", Suffrage: "nonvoter", UnreachableFor: 10 * time.Minute},
			},
			voters:    1,
			hasQuorum: true,
		},
		{
			name: "demote an unreachable voter when there is quorum",
			peers: []peerHealth{
				{ID: "a", Suffrage: "voter", UnreachableFor: demoteAfter},
				{ID: "b", Suffrage: "voter", ReachableFor: time.Minute},
			},
			voters:     3, // self + a + b
			hasQuorum:  true,
			wantDemote: []string{"a"},
		},
		{
			name: "do not demote without quorum (2-node lost-a-voter case)",
			peers: []peerHealth{
				{ID: "a", Suffrage: "voter", UnreachableFor: demoteAfter},
			},
			voters:    2, // self + a, only self reachable
			hasQuorum: false,
		},
		{
			name: "do not demote a voter unreachable for too short",
			peers: []peerHealth{
				{ID: "a", Suffrage: "voter", UnreachableFor: demoteAfter - time.Second},
			},
			voters:    3,
			hasQuorum: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			promote, demote := decideMembership(tc.peers, tc.voters, tc.hasQuorum, promoteAfter, demoteAfter)
			if !eqStrings(promote, tc.wantPromote) {
				t.Errorf("promote = %v, want %v", promote, tc.wantPromote)
			}
			if !eqStrings(demote, tc.wantDemote) {
				t.Errorf("demote = %v, want %v", demote, tc.wantDemote)
			}
		})
	}
}

func eqStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// fakeMemSvc is a Service stub that records membership mutations.
type fakeMemSvc struct {
	leader  bool
	status  Status
	added   []string
	demoted []string
}

func (f *fakeMemSvc) SubmitCommand(context.Context, Command, time.Duration) (SubmitResult, error) {
	return SubmitResult{}, nil
}
func (f *fakeMemSvc) Status() Status                   { return f.status }
func (f *fakeMemSvc) Enabled() bool                    { return true }
func (f *fakeMemSvc) IsLeader() bool                   { return f.leader }
func (f *fakeMemSvc) AddVoter(id, _ string) error      { f.added = append(f.added, id); return nil }
func (f *fakeMemSvc) AddNonvoter(string, string) error { return nil }
func (f *fakeMemSvc) DemoteVoter(id string) error      { f.demoted = append(f.demoted, id); return nil }
func (f *fakeMemSvc) RemovePeer(string) error          { return nil }
func (f *fakeMemSvc) TransferLeadership() error        { return nil }
func (f *fakeMemSvc) Stats() map[string]string         { return nil }
func (f *fakeMemSvc) Close() error                     { return nil }

func newTestManager(svc Service, reachable map[string]bool) *MembershipManager {
	m := NewMembershipManager(svc, nil)
	m.dial = func(addr string) bool { return reachable[addr] }
	return m
}

func TestMembershipTickNonLeaderNoOp(t *testing.T) {
	svc := &fakeMemSvc{
		leader: false,
		status: Status{NodeID: "self", Peers: []Peer{
			{ID: "self", Addr: "10.0.0.1:7000", Suffrage: "voter"},
			{ID: "b", Addr: "10.0.0.2:7000", Suffrage: "nonvoter"},
		}},
	}
	m := newTestManager(svc, map[string]bool{"10.0.0.2:7000": true})
	m.tick(time.Now())
	if len(svc.added) != 0 || len(svc.demoted) != 0 {
		t.Fatalf("non-leader must not change membership: added=%v demoted=%v", svc.added, svc.demoted)
	}
}

func TestMembershipTickPromotesHealthyNonvoter(t *testing.T) {
	svc := &fakeMemSvc{
		leader: true,
		status: Status{NodeID: "self", Peers: []Peer{
			{ID: "self", Addr: "10.0.0.1:7000", Suffrage: "voter"},
			{ID: "b", Addr: "10.0.0.2:7000", Suffrage: "nonvoter"},
		}},
	}
	m := newTestManager(svc, map[string]bool{"10.0.0.2:7000": true})

	t0 := time.Now()
	m.tick(t0) // first sighting: ReachableFor == 0, no promotion yet
	if len(svc.added) != 0 {
		t.Fatalf("should not promote on first reachable tick: %v", svc.added)
	}
	m.tick(t0.Add(membershipPromoteAfter)) // sustained reachable → promote
	if !eqStrings(svc.added, []string{"b"}) {
		t.Fatalf("expected b promoted, got %v", svc.added)
	}
}

func TestMembershipTickDemotesUnreachableVoterWithQuorum(t *testing.T) {
	svc := &fakeMemSvc{
		leader: true,
		status: Status{NodeID: "self", Peers: []Peer{
			{ID: "self", Addr: "10.0.0.1:7000", Suffrage: "voter"},
			{ID: "a", Addr: "10.0.0.2:7000", Suffrage: "voter"}, // unreachable
			{ID: "c", Addr: "10.0.0.3:7000", Suffrage: "voter"}, // reachable → quorum holds
		}},
	}
	m := newTestManager(svc, map[string]bool{"10.0.0.3:7000": true})

	t0 := time.Now()
	m.tick(t0)
	if len(svc.demoted) != 0 {
		t.Fatalf("should not demote on first unreachable tick: %v", svc.demoted)
	}
	m.tick(t0.Add(membershipDemoteAfter))
	if !eqStrings(svc.demoted, []string{"a"}) {
		t.Fatalf("expected a demoted, got %v", svc.demoted)
	}
}

func TestMembershipTickKeepsUnreachableVoterWithoutQuorum(t *testing.T) {
	// 2-voter cluster, the only peer is unreachable: demoting it can't commit
	// (no quorum), so the manager must leave it for the manual recovery path.
	svc := &fakeMemSvc{
		leader: true,
		status: Status{NodeID: "self", Peers: []Peer{
			{ID: "self", Addr: "10.0.0.1:7000", Suffrage: "voter"},
			{ID: "a", Addr: "10.0.0.2:7000", Suffrage: "voter"},
		}},
	}
	m := newTestManager(svc, map[string]bool{}) // a unreachable

	t0 := time.Now()
	m.tick(t0)
	m.tick(t0.Add(membershipDemoteAfter))
	if len(svc.demoted) != 0 {
		t.Fatalf("must not demote without quorum: %v", svc.demoted)
	}
}
