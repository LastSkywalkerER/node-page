package hosts

import (
	"testing"
	"time"
)

func TestNodeAlertStore_SetGetClearExpire(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 11, 10, 0, 0, 0, time.UTC)
	s := NewNodeAlertStore()
	s.now = func() time.Time { return now }

	if got := s.NodeAlert(9); got != nil {
		t.Fatalf("empty store returned %+v", got)
	}

	s.Set(9, NodeAlert{Kind: NodeAlertRaftIsolated, Title: "cut off"})
	got := s.NodeAlert(9)
	if got == nil || got.Title != "cut off" {
		t.Fatalf("after Set: got %+v", got)
	}
	// The returned value is a copy: mutating it must not touch the store.
	got.Title = "mutated"
	if again := s.NodeAlert(9); again.Title != "cut off" {
		t.Fatalf("store leaked its internal value: %q", again.Title)
	}

	// A refresh keeps it alive past the original TTL.
	now = now.Add(NodeAlertTTL - time.Second)
	s.Set(9, NodeAlert{Kind: NodeAlertRaftIsolated, Title: "still cut off"})
	now = now.Add(NodeAlertTTL - time.Second)
	if got := s.NodeAlert(9); got == nil || got.Title != "still cut off" {
		t.Fatalf("refreshed alert should still be visible, got %+v", got)
	}

	// Past the TTL without a refresh it is gone (the node went silent).
	now = now.Add(2 * time.Second)
	if got := s.NodeAlert(9); got != nil {
		t.Fatalf("expired alert still served: %+v", got)
	}

	s.Set(9, NodeAlert{Title: "back"})
	s.Clear(9)
	if got := s.NodeAlert(9); got != nil {
		t.Fatalf("cleared alert still served: %+v", got)
	}

	// Nil receiver is a no-op everywhere (the store is optional wiring).
	var nilStore *NodeAlertStore
	nilStore.Set(1, NodeAlert{})
	nilStore.Clear(1)
	if nilStore.NodeAlert(1) != nil {
		t.Fatal("nil store returned an alert")
	}
}

// TestNodeAlertStore_ReportsTransitions: the store tells a caller whether the
// news is NEW, so a fault that lasts for days is logged once, not on every
// batch that repeats it.
func TestNodeAlertStore_ReportsTransitions(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	s := NewNodeAlertStore()
	s.now = func() time.Time { return now }

	if !s.Set(9, NodeAlert{Kind: NodeAlertRaftIsolated, Title: "cut off"}) {
		t.Fatal("the first alert for a host is news")
	}
	now = now.Add(20 * time.Second)
	if s.Set(9, NodeAlert{Kind: NodeAlertRaftIsolated, Title: "cut off"}) {
		t.Fatal("a repeat of the same fault is not news")
	}
	if !s.Set(9, NodeAlert{Kind: NodeAlertRaftIsolated, Title: "advertises a dead address"}) {
		t.Fatal("a different fault on the same host is news")
	}
	// A refresh after the alert had expired counts as news again.
	now = now.Add(NodeAlertTTL + time.Second)
	if !s.Set(9, NodeAlert{Kind: NodeAlertRaftIsolated, Title: "advertises a dead address"}) {
		t.Fatal("an alert that had expired is news when it comes back")
	}

	if !s.Clear(9) {
		t.Fatal("clearing a live alert must report that there was one")
	}
	if s.Clear(9) {
		t.Fatal("clearing nothing must report nothing")
	}
	now = now.Add(time.Second)
	s.Set(9, NodeAlert{Title: "x"})
	now = now.Add(NodeAlertTTL + time.Second)
	if s.Clear(9) {
		t.Fatal("an already-expired alert is not a live one to clear")
	}
}
