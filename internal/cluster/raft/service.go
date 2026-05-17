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

// Close is a no-op.
func (DisabledService) Close() error { return nil }
