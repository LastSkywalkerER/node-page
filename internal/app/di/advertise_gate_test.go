package di

import (
	"errors"
	"testing"
	"time"
)

const (
	testResync = 10 * time.Minute
	testRetry  = 30 * time.Second
)

// TestAdvertiseGate_RetriesAFailedPublish is the regression for a node that
// stayed invisible to its peers for a full resync period: it advertised once
// while it had no leader (the normal state of a node waiting to be added), the
// write failed, and the loop recorded it as published. No peer then knew its
// URL, so none streamed metrics to it and its dashboard showed every other
// machine as empty.
func TestAdvertiseGate_RetriesAFailedPublish(t *testing.T) {
	t.Parallel()
	g := newAdvertiseGate(testResync, testRetry)
	now := time.Now()
	const url = "http://10.0.0.5:9090"
	boom := errors.New("raft: no known leader to forward to")

	if !g.due(now, url, false) {
		t.Fatal("the first publish must happen")
	}
	g.record(now, url, boom)

	// Not on the very next tick — a node that cannot reach a leader at all
	// must not hammer it.
	now = now.Add(3 * time.Second)
	if g.due(now, url, false) {
		t.Fatal("a failed publish must back off, not retry every tick")
	}
	now = now.Add(testRetry - 3*time.Second)
	if !g.due(now, url, false) {
		t.Fatal("a failed publish must be retried once the back-off elapses")
	}
	g.record(now, url, nil) // the leader is there now

	// Published: quiet until something changes or the slow resync elapses.
	now = now.Add(time.Minute)
	if g.due(now, url, false) {
		t.Fatal("an unchanged URL must not be republished — each publish is a consensus round")
	}
	now = now.Add(testResync)
	if !g.due(now, url, false) {
		t.Fatal("the safety resync must still fire")
	}
}

func TestAdvertiseGate_ChangeAndLeadership(t *testing.T) {
	t.Parallel()
	g := newAdvertiseGate(testResync, testRetry)
	now := time.Now()
	g.record(now, "http://10.0.0.5:9090", nil)

	now = now.Add(5 * time.Second)
	if !g.due(now, "http://10.0.0.9:9090", false) {
		t.Fatal("a changed URL must be published at once")
	}
	// Gaining leadership republishes immediately: followers forward writes to
	// the leader's URL, so it has to be in the catalog before they try.
	if !g.due(now, "http://10.0.0.5:9090", true) {
		t.Fatal("gaining leadership must publish at once")
	}
	// An empty URL (nothing configured or derivable) is not a change.
	if g.due(now, "", false) {
		t.Fatal("an empty URL must not trigger a publish")
	}
	// Even leadership can't shortcut the back-off after a failure.
	g.record(now, "http://10.0.0.9:9090", errors.New("nope"))
	if g.due(now.Add(time.Second), "http://10.0.0.9:9090", true) {
		t.Fatal("the back-off applies to the leadership trigger too")
	}
}
