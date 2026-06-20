package raft

import (
	"context"
	"net"
	"time"

	"github.com/charmbracelet/log"
)

// Membership tuning. New nodes join as NON-voters (they replicate but don't
// count toward quorum), so a botched or unreachable new node can never cost
// the cluster its write quorum on join. The leader promotes a non-voter to
// voter only after it has stayed reachable for promoteAfter. A voter that then
// goes unreachable for demoteAfter is demoted back to a non-voter to restore
// quorum among the reachable voters — but only while the cluster still has a
// quorum (the demote config-change itself needs to commit), so the 2-node
// "lost a voter" case is left to the manual recovery action instead.
const (
	membershipInterval     = 10 * time.Second
	membershipPromoteAfter = 20 * time.Second
	membershipDemoteAfter  = 90 * time.Second
	membershipDialTimeout  = 2 * time.Second
)

// peerHealth is the per-peer input to the membership decision.
type peerHealth struct {
	ID             string
	Addr           string
	Suffrage       string        // "voter" | "nonvoter"
	ReachableFor   time.Duration // >0 while continuously reachable, else 0
	UnreachableFor time.Duration // >0 while continuously unreachable, else 0
}

// decideMembership is the pure promotion/demotion policy. `peers` must already
// exclude self (self is always a reachable voter — the leader). `voters` is the
// total voter count including self; `hasQuorum` reports whether a majority of
// voters is currently reachable (a config change can only commit when true).
//
//   - promote a non-voter that has been reachable >= promoteAfter
//   - demote a voter that has been unreachable >= demoteAfter, only with quorum
//
// Self is never demoted (it isn't in `peers`), so at least one voter always
// remains.
func decideMembership(peers []peerHealth, voters int, hasQuorum bool, promoteAfter, demoteAfter time.Duration) (promote, demote []string) {
	for _, p := range peers {
		switch p.Suffrage {
		case "nonvoter":
			if p.ReachableFor >= promoteAfter {
				promote = append(promote, p.ID)
			}
		case "voter":
			if hasQuorum && p.UnreachableFor >= demoteAfter {
				demote = append(demote, p.ID)
			}
		}
	}
	return promote, demote
}

// MembershipManager runs on every node but only acts while this node is the
// leader: it watches peer reachability and promotes healthy non-voters / demotes
// long-unreachable voters via the Service. It holds a reference to the (swappable)
// Service so it keeps working across a wipe-state re-activation.
type MembershipManager struct {
	svc    Service
	logger *log.Logger
	dial   func(addr string) bool

	interval     time.Duration
	promoteAfter time.Duration
	demoteAfter  time.Duration

	// since[id] tracks when the current reachable/unreachable streak began.
	reachableSince   map[string]time.Time
	unreachableSince map[string]time.Time
}

// NewMembershipManager builds a manager driven by the given Service.
func NewMembershipManager(svc Service, logger *log.Logger) *MembershipManager {
	return &MembershipManager{
		svc:              svc,
		logger:           logger,
		dial:             dialReachable,
		interval:         membershipInterval,
		promoteAfter:     membershipPromoteAfter,
		demoteAfter:      membershipDemoteAfter,
		reachableSince:   map[string]time.Time{},
		unreachableSince: map[string]time.Time{},
	}
}

// dialReachable reports whether a fresh TCP connection to addr succeeds.
func dialReachable(addr string) bool {
	conn, err := net.DialTimeout("tcp", addr, membershipDialTimeout)
	if conn != nil {
		_ = conn.Close()
	}
	return err == nil
}

// Run drives the manager until ctx is cancelled. Safe to call once per process.
func (m *MembershipManager) Run(ctx context.Context) {
	t := time.NewTicker(m.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		m.tick(time.Now())
	}
}

// tick performs one reconciliation pass. Exposed (unexported) with an explicit
// `now` so the loop body is deterministic in tests via the injected dialer.
func (m *MembershipManager) tick(now time.Time) {
	if m.svc == nil || !m.svc.Enabled() || !m.svc.IsLeader() {
		// Only the leader changes membership; forget streaks so a node that
		// later (re)gains leadership starts measuring from scratch.
		if len(m.reachableSince) > 0 || len(m.unreachableSince) > 0 {
			m.reachableSince = map[string]time.Time{}
			m.unreachableSince = map[string]time.Time{}
		}
		return
	}

	st := m.svc.Status()
	self := st.NodeID

	// Probe every non-self peer and refresh its reachability streak.
	live := map[string]struct{}{}
	voters := 0
	reachableVoters := 1 // self is a reachable voter (we are the leader)
	peers := make([]peerHealth, 0, len(st.Peers))
	for _, p := range st.Peers {
		if p.Suffrage == "voter" {
			voters++
		}
		if p.ID == self {
			continue
		}
		live[p.ID] = struct{}{}
		reachable := m.dial(p.Addr)
		if reachable {
			if _, ok := m.reachableSince[p.ID]; !ok {
				m.reachableSince[p.ID] = now
			}
			delete(m.unreachableSince, p.ID)
			if p.Suffrage == "voter" {
				reachableVoters++
			}
		} else {
			if _, ok := m.unreachableSince[p.ID]; !ok {
				m.unreachableSince[p.ID] = now
			}
			delete(m.reachableSince, p.ID)
		}
		ph := peerHealth{ID: p.ID, Addr: p.Addr, Suffrage: p.Suffrage}
		if since, ok := m.reachableSince[p.ID]; ok {
			ph.ReachableFor = now.Sub(since)
		}
		if since, ok := m.unreachableSince[p.ID]; ok {
			ph.UnreachableFor = now.Sub(since)
		}
		peers = append(peers, ph)
	}

	// Drop streak state for peers that left the configuration.
	for id := range m.reachableSince {
		if _, ok := live[id]; !ok {
			delete(m.reachableSince, id)
		}
	}
	for id := range m.unreachableSince {
		if _, ok := live[id]; !ok {
			delete(m.unreachableSince, id)
		}
	}

	hasQuorum := reachableVoters*2 > voters
	promote, demote := decideMembership(peers, voters, hasQuorum, m.promoteAfter, m.demoteAfter)

	addrByID := make(map[string]string, len(peers))
	for _, p := range peers {
		addrByID[p.ID] = p.Addr
	}
	for _, id := range promote {
		if err := m.svc.AddVoter(id, addrByID[id]); err != nil {
			if m.logger != nil {
				m.logger.Warn("raft membership: promote to voter failed", "node_id", id, "error", err)
			}
		} else if m.logger != nil {
			m.logger.Info("raft membership: promoted node to voter", "node_id", id)
		}
	}
	for _, id := range demote {
		if err := m.svc.DemoteVoter(id); err != nil {
			if m.logger != nil {
				m.logger.Warn("raft membership: demote unreachable voter failed", "node_id", id, "error", err)
			}
		} else if m.logger != nil {
			m.logger.Info("raft membership: demoted unreachable voter to non-voter", "node_id", id)
		}
	}
}
