package hosts

import (
	"sync"
	"time"
)

// NodeAlert kinds.
const (
	// NodeAlertRaftIsolated: the node-stats node on this host is cut off from
	// its Raft cluster — peers cannot reach its advertised Raft address, so it
	// hears no leader, cannot commit or forward writes, and its replicated host
	// record is frozen. Its metrics still arrive over the off-Raft stream.
	NodeAlertRaftIsolated = "raft_isolated"
)

// NodeAlert severities.
const (
	NodeAlertSeverityWarning = "warning"
	NodeAlertSeverityError   = "error"
)

// NodeAlert is a fault the node-stats NODE running on a host diagnosed about
// itself — about the node, not the machine. It rides the best-effort metric
// stream (the one channel a Raft-isolated node still has to its peers and to a
// bridged hub) so every dashboard can mark that machine's card and tell the
// operator what is wrong and how to re-attach the node. Never persisted: it
// lives in RAM on each receiver and expires when the node stops sending it.
type NodeAlert struct {
	Kind     string `json:"kind"`
	Severity string `json:"severity"`
	// Title is the one-line headline; Detail explains what the node observed.
	Title  string `json:"title"`
	Detail string `json:"detail"`
	// Action is ONE short sentence naming what to do — the only text compact
	// surfaces (a machine card's tooltip, a row in the node list) show. Steps
	// carry the same remedy in full, for the page the operator lands on.
	Action string `json:"action,omitempty"`
	// Steps are the operator's options, in order of preference.
	Steps []string `json:"steps,omitempty"`

	// Diagnostic facts the UI may render in monospace.
	NodeID        string `json:"node_id,omitempty"`
	AdvertiseAddr string `json:"advertise_addr,omitempty"`
	AdvertiseURL  string `json:"advertise_url,omitempty"`
	// LocalIPv4 is where the machine actually is (its default-route address)
	// when that differs from what it advertises; "" when unknown.
	LocalIPv4 string `json:"local_ipv4,omitempty"`
	// NodeURL is where THIS node's own dashboard actually answers now, so the
	// UI can send the operator to the settings page of the machine that needs
	// the fix. Deliberately not the advertised URL when that is the stale
	// value being complained about — it would link into the void.
	NodeURL string `json:"node_url,omitempty"`

	// Since is when the node first detected the condition.
	Since time.Time `json:"since"`
}

// NodeAlertSource yields the current alert for a host row, nil when none.
type NodeAlertSource interface {
	NodeAlert(hostID uint) *NodeAlert
}

// NodeAlertTTL is how long a received alert stays visible without being
// refreshed. An isolated node re-sends it with every (throttled) metric batch;
// a node that fixed itself sends batches without it and clears it at once,
// and a node that went silent drops off the card along with its liveness.
const NodeAlertTTL = 3 * time.Minute

// NodeAlertStore keeps the latest alert per LOCAL host id in RAM. Deliberately
// not a table: the state is transient, tiny, and must never enter Raft (the
// isolated node cannot write there anyway).
type NodeAlertStore struct {
	mu     sync.RWMutex
	byHost map[uint]storedNodeAlert
	ttl    time.Duration
	now    func() time.Time
}

type storedNodeAlert struct {
	alert NodeAlert
	at    time.Time
}

// NewNodeAlertStore builds an empty store with the default TTL.
func NewNodeAlertStore() *NodeAlertStore {
	return &NodeAlertStore{byHost: map[uint]storedNodeAlert{}, ttl: NodeAlertTTL, now: time.Now}
}

// Set records (or refreshes) the alert for a host. It reports whether this is
// NEW news — a host that had no live alert, or a different fault than before —
// so callers can log the transition instead of every refresh (an affected node
// re-sends the same alert with every batch).
func (s *NodeAlertStore) Set(hostID uint, alert NodeAlert) (changed bool) {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	prev, had := s.byHost[hostID]
	changed = !had || now.Sub(prev.at) > s.ttl ||
		prev.alert.Kind != alert.Kind || prev.alert.Title != alert.Title
	s.byHost[hostID] = storedNodeAlert{alert: alert, at: now}
	return changed
}

// Clear drops the alert for a host (the node reports itself healthy again) and
// reports whether there was a live one to drop.
func (s *NodeAlertStore) Clear(hostID uint) (had bool) {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	prev, ok := s.byHost[hostID]
	had = ok && s.now().Sub(prev.at) <= s.ttl
	delete(s.byHost, hostID)
	return had
}

// NodeAlert implements NodeAlertSource: the host's alert unless it expired.
func (s *NodeAlertStore) NodeAlert(hostID uint) *NodeAlert {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	st, ok := s.byHost[hostID]
	s.mu.RUnlock()
	if !ok {
		return nil
	}
	if s.now().Sub(st.at) > s.ttl {
		s.mu.Lock()
		// Re-check under the write lock: a refresh may have landed meanwhile.
		if cur, still := s.byHost[hostID]; still && s.now().Sub(cur.at) > s.ttl {
			delete(s.byHost, hostID)
		}
		s.mu.Unlock()
		return nil
	}
	a := st.alert
	return &a
}
