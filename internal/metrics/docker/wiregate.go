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

// ShouldSend reports whether the metric should be included in the outgoing
// batch now, and records the decision. It hashes the inventory straight from
// the struct, so on a gated (idle) tick the payload is never marshaled at all.
func (g *WireGate) ShouldSend(m *DockerMetric, now time.Time) bool {
	if m == nil {
		return false
	}
	h := InventoryHash(m)
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

// InventoryHash hashes the docker metric EXCLUDING volatile per-tick runtime
// fields (Stats counters, the "Up 5 minutes" Status string), so an idle
// inventory hashes the same tick-to-tick while any structural change — count,
// state, image, ports, labels, sizes, update flags — changes the hash.
//
// The metric is copied shallowly (stacks + containers slices) with the
// volatile fields blanked; the shared maps/slices inside a container are
// never mutated.
func InventoryHash(m *DockerMetric) uint64 {
	cp := *m
	cp.Stacks = make([]DockerStack, len(m.Stacks))
	for si := range m.Stacks {
		st := m.Stacks[si]
		st.Containers = make([]DockerContainer, len(m.Stacks[si].Containers))
		for ci := range m.Stacks[si].Containers {
			c := m.Stacks[si].Containers[ci]
			c.Stats = DockerStats{}
			c.Status = ""
			st.Containers[ci] = c
		}
		cp.Stacks[si] = st
	}
	h := fnv.New64a()
	enc := json.NewEncoder(hashWriter{h})
	if err := enc.Encode(cp); err != nil {
		// Unencodable metric: fall back to the raw struct's textual form so
		// behaviour degrades to change-detection on that.
		h.Reset()
		_, _ = h.Write([]byte(err.Error()))
	}
	return h.Sum64()
}

// hashWriter streams the JSON encoding into the hash without buffering the
// whole document.
type hashWriter struct {
	h interface{ Write([]byte) (int, error) }
}

func (w hashWriter) Write(p []byte) (int, error) { return w.h.Write(p) }
