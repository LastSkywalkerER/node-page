package di

import "time"

// advertiseGate decides when the self-advertise loop publishes this node's URL
// into the replicated catalog, and remembers whether the last attempt actually
// landed.
//
// The catalog row is what peers POST metrics to, so a node missing from it is
// a node no peer streams to: every other machine's card stays empty on its
// dashboard, and its own metrics are ignored by peers that never learned its
// URL. The first attempt normally FAILS — a node runs before an admin adds it
// and has no leader to forward the write to — so recording that attempt as a
// publish (what this used to do) hid the node for a full resync period.
//
// Publishing is a consensus round, so it must stay rare: only a change, a
// fresh leadership, or the slow resync triggers it, and a failed attempt is
// retried on a fixed back-off rather than every tick (a node genuinely cut off
// from its cluster would otherwise hammer the leader's URL forever).
type advertiseGate struct {
	resync     time.Duration
	retryAfter time.Duration

	lastURL     string
	lastAt      time.Time
	lastAttempt time.Time
	failing     bool
}

func newAdvertiseGate(resync, retryAfter time.Duration) *advertiseGate {
	return &advertiseGate{resync: resync, retryAfter: retryAfter}
}

// due reports whether to publish now. gainedLeadership is true on the tick this
// node just became leader (its URL must land at once — followers forward their
// writes to it).
func (g *advertiseGate) due(now time.Time, url string, gainedLeadership bool) bool {
	due := gainedLeadership ||
		(url != "" && url != g.lastURL) ||
		now.Sub(g.lastAt) >= g.resync
	if due && g.failing && now.Sub(g.lastAttempt) < g.retryAfter {
		return false // still backing off from a failed attempt
	}
	return due
}

// record takes the outcome of a publish. On failure the gate keeps the attempt
// pending, so the next due check (past the back-off) tries again.
func (g *advertiseGate) record(now time.Time, url string, err error) {
	g.lastAttempt = now
	if err != nil {
		g.failing = true
		return
	}
	g.failing = false
	g.lastURL = url
	g.lastAt = now
}
