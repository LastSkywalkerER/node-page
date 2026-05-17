package raft

import "context"

// WriteGate decides whether a repository write should proceed directly against
// the underlying database, or be rejected with ErrMustSubmitViaRaft so the
// caller routes through Service.SubmitCommand.
//
// When Raft is disabled the gate always allows writes (legacy behaviour). When
// Raft is enabled the gate allows writes only from the FSM applier goroutine
// (identified by IsApplier(ctx)).
type WriteGate interface {
	// Allow reports whether ctx is permitted to perform a direct write.
	Allow(ctx context.Context) bool
}

// OpenGate is a WriteGate that always allows direct writes. It is used when
// RAFT_ENABLED=false so the legacy code path keeps working unchanged.
type OpenGate struct{}

// Allow implements WriteGate; always returns true.
func (OpenGate) Allow(ctx context.Context) bool { return true }

// raftGate gates writes when Raft is enabled — only FSM applier contexts pass.
type raftGate struct{}

// Allow implements WriteGate; returns true only for applier-stamped contexts.
func (raftGate) Allow(ctx context.Context) bool { return IsApplier(ctx) }

// NewWriteGate returns the appropriate WriteGate for the given enabled flag.
func NewWriteGate(raftEnabled bool) WriteGate {
	if raftEnabled {
		return raftGate{}
	}
	return OpenGate{}
}
