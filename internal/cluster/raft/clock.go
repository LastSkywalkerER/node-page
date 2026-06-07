package raft

import "time"

// Clock abstracts time.Now so FSM.Apply can be deterministic — every replica
// derives every wall-clock value from the leader-stamped Command.Timestamp, not
// from its own clock. The production binding (SystemClock) is used by the
// submitter side only.
type Clock interface {
	Now() time.Time
}

// SystemClock returns time.Now(). Use it on the submitter side (leader).
type SystemClock struct{}

// Now implements Clock.
func (SystemClock) Now() time.Time { return time.Now().UTC() }
