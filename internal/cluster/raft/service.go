package raft

import (
	"context"
	"time"
)

// Service is the high-level API the rest of the app talks to. Callers do not
// know whether the underlying implementation is a real HashiCorp Raft node or
// the disabled no-op — they just submit Commands and read Status.
type Service interface {
	// SubmitCommand encodes a Command, replicates it through Raft and waits
	// for FSM.Apply to acknowledge. If this node is a follower the call is
	// forwarded over HTTPS to the current leader.
	SubmitCommand(ctx context.Context, cmd Command, timeout time.Duration) (SubmitResult, error)

	// Status returns a snapshot of the local node's view of the cluster.
	Status() Status

	// Enabled reports whether the Raft layer is active.
	Enabled() bool

	// IsLeader reports whether the local node currently holds leadership.
	IsLeader() bool

	// AddVoter adds a peer Raft voter. Leader-only.
	AddVoter(id, addr string) error

	// AddNonvoter adds a peer as a non-voting member (replicates but does not
	// count toward quorum). New nodes join this way. Leader-only.
	AddNonvoter(id, addr string) error

	// DemoteVoter turns a voter back into a non-voter. Leader-only; needs the
	// current quorum to commit.
	DemoteVoter(id string) error

	// RemovePeer removes a peer Raft server. Leader-only.
	RemovePeer(id string) error

	// TransferLeadership hands leadership to a healthy follower and blocks until
	// the transfer completes. Leader-only; used so a leaving leader can step down
	// before being removed (avoids the fragile "leader removes itself" path).
	TransferLeadership() error

	// Stats returns the raw hashicorp/raft Stats map for diagnostics
	// (term, last_contact, num_peers, leader, applied/commit indices,
	// snapshot info, etc). Nil when the service is disabled.
	Stats() map[string]string

	// Close shuts the layer down cleanly. Safe to call on a disabled service.
	Close() error
}

// DisabledService is the implementation used when RAFT_ENABLED=false.
// SubmitCommand always returns ErrDisabled so the caller knows to fall back to
// the direct-write path.
type DisabledService struct{}

// NewDisabledService returns a Service that reports Raft as disabled.
func NewDisabledService() Service { return DisabledService{} }

// SubmitCommand always errors with ErrDisabled.
func (DisabledService) SubmitCommand(ctx context.Context, cmd Command, timeout time.Duration) (SubmitResult, error) {
	return SubmitResult{}, ErrDisabled
}

// Status reports the layer as disabled.
func (DisabledService) Status() Status {
	return Status{Enabled: false}
}

// Enabled returns false.
func (DisabledService) Enabled() bool { return false }

// IsLeader returns false on the disabled service.
func (DisabledService) IsLeader() bool { return false }

// AddVoter is a no-op for the disabled service.
func (DisabledService) AddVoter(id, addr string) error { return ErrDisabled }

// AddNonvoter is a no-op for the disabled service.
func (DisabledService) AddNonvoter(id, addr string) error { return ErrDisabled }

// DemoteVoter is a no-op for the disabled service.
func (DisabledService) DemoteVoter(id string) error { return ErrDisabled }

// RemovePeer is a no-op for the disabled service.
func (DisabledService) RemovePeer(id string) error { return ErrDisabled }

// TransferLeadership is a no-op for the disabled service.
func (DisabledService) TransferLeadership() error { return ErrDisabled }

// Stats returns nil on the disabled service.
func (DisabledService) Stats() map[string]string { return nil }

// Close is a no-op.
func (DisabledService) Close() error { return nil }
