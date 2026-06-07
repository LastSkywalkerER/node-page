package raft

import "errors"

// ErrMustSubmitViaRaft is returned by repository writers when Raft is enabled
// and the caller is not the FSM applier. Callers should translate the write
// into a Command and submit it through Service.SubmitCommand instead of
// touching the underlying SQL store directly.
var ErrMustSubmitViaRaft = errors.New("raft: write must be submitted via Service.SubmitCommand")

// ErrDisabled is returned by Service methods when the Raft layer is disabled
// (RAFT_ENABLED=false). Callers should fall back to the legacy direct-write
// path in that case.
var ErrDisabled = errors.New("raft: layer is disabled")

// ErrNotLeader is returned when a write is attempted on a follower with no
// known leader to forward to. The caller may retry after a short backoff.
var ErrNotLeader = errors.New("raft: not the leader and no known leader to forward to")

// ErrUnknownCommand is returned (and logged) when FSM.Apply receives a
// Command.Type it does not recognise. It is treated as a deterministic error —
// the log index advances on every replica identically.
var ErrUnknownCommand = errors.New("raft: unknown command type")
