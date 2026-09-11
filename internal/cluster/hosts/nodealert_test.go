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
