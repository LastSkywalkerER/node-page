package docker

import (
	"encoding/json"
	"hash/fnv"
	"sync"
	"time"
)

// WireGate decides whether the full docker payload rides this tick's cluster
// metric batch. The payload is static-heavy (labels/ports/image/mounts per
// container) and dominates the metric-stream bytes, yet its INVENTORY —
// everything except per-tick runtime stats — barely changes. The gate sends
// the payload when that inventory changed (a container appeared/vanished,
// state flipped, image/ports/labels moved) and otherwise only on the resync
// cadence, which doubles as the self-heal for the best-effort stream (a
// dropped batch is replaced by the next resync) and bounds how stale remote
// viewers' container stats can get.
//
// Wire-compatible by construction: receivers already handle a batch with or
// without the docker field, so a mixed-version cluster just sees the payload
// less often.
type WireGate struct {
	mu       sync.Mutex
	lastHash uint64
	sentOnce bool
	lastSent time.Time
	resync   time.Duration
}

// NewWireGate builds a gate with the given resync cadence.
func NewWireGate(resync time.Duration) *WireGate {
	return &WireGate{resync: resync}
}

// ShouldSend reports whether payload (the marshaled DockerMetric) should be
// included in the outgoing batch now, and records the decision.
func (g *WireGate) ShouldSend(payload []byte, now time.Time) bool {
	h := inventoryHash(payload)
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.sentOnce && h == g.lastHash && now.Sub(g.lastSent) < g.resync {
		return false
	}
	g.lastHash = h
	g.lastSent = now
	g.sentOnce = true
	return true
}

// inventoryHash hashes the docker payload EXCLUDING volatile per-tick runtime
// fields (Stats counters, the "Up 5 minutes" Status string), so an idle
// inventory hashes the same tick-to-tick while any structural change — count,
// state, image, ports, labels, sizes, update flags — changes the hash.
func inventoryHash(payload []byte) uint64 {
	var m DockerMetric
	if err := json.Unmarshal(payload, &m); err != nil {
		// Undecodable payload: hash the raw bytes so behavior degrades to
		// change-detection on the exact payload.
		h := fnv.New64a()
		_, _ = h.Write(payload)
		return h.Sum64()
	}
	for si := range m.Stacks {
		for ci := range m.Stacks[si].Containers {
			c := &m.Stacks[si].Containers[ci]
			c.Stats = DockerStats{}
			c.Status = ""
		}
	}
	b, err := json.Marshal(m)
	if err != nil {
		h := fnv.New64a()
		_, _ = h.Write(payload)
		return h.Sum64()
	}
	h := fnv.New64a()
	_, _ = h.Write(b)
	return h.Sum64()
}
