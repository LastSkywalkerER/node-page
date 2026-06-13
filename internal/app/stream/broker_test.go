package stream

import (
	"testing"
	"time"
)

// TestCloseAllUnblocksSubscribers verifies that CloseAll closes every
// subscriber channel so a handler blocked on a receive unblocks with ok==false.
func TestCloseAllUnblocksSubscribers(t *testing.T) {
	b := NewBroker()
	ch := b.Subscribe()

	done := make(chan bool, 1)
	go func() {
		_, ok := <-ch // blocks until CloseAll closes the channel
		done <- ok
	}()

	b.CloseAll()

	select {
	case ok := <-done:
		if ok {
			t.Fatalf("expected channel closed (ok=false), got ok=true")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("subscriber did not unblock after CloseAll")
	}
}

// TestSubscribeAfterCloseReturnsClosedChannel verifies a late Subscribe returns
// an already-closed channel so the caller's receive loop exits immediately.
func TestSubscribeAfterCloseReturnsClosedChannel(t *testing.T) {
	b := NewBroker()
	b.CloseAll()

	ch := b.Subscribe()
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatalf("expected closed channel from Subscribe after CloseAll")
		}
	case <-time.After(time.Second):
		t.Fatal("Subscribe after CloseAll returned an open channel")
	}
}

// TestDoneClosedOnCloseAll verifies the Done channel signals shutdown so the
// keepalive-only loop can exit.
func TestDoneClosedOnCloseAll(t *testing.T) {
	b := NewBroker()

	select {
	case <-b.Done():
		t.Fatal("Done closed before CloseAll")
	default:
	}

	b.CloseAll()

	select {
	case <-b.Done():
	case <-time.After(time.Second):
		t.Fatal("Done not closed after CloseAll")
	}
}

// TestUnsubscribeAfterCloseAllIsNoop verifies a handler's deferred Unsubscribe
// after CloseAll does not panic on a double close.
func TestUnsubscribeAfterCloseAllIsNoop(t *testing.T) {
	b := NewBroker()
	ch := b.Subscribe()

	b.CloseAll()
	// Channel was already closed/removed by CloseAll; this must be a no-op.
	b.Unsubscribe(ch)
}

// TestCloseAllIdempotent verifies a second CloseAll does not panic.
func TestCloseAllIdempotent(t *testing.T) {
	b := NewBroker()
	b.Subscribe()
	b.CloseAll()
	b.CloseAll()
}

// TestPublishThenUnsubscribeStillWorks sanity-checks the normal path is intact.
func TestPublishThenUnsubscribeStillWorks(t *testing.T) {
	b := NewBroker()
	ch := b.Subscribe()

	b.Publish([]byte("hello"))
	select {
	case got := <-ch:
		if string(got) != "hello" {
			t.Fatalf("got %q, want %q", got, "hello")
		}
	case <-time.After(time.Second):
		t.Fatal("did not receive published message")
	}

	b.Unsubscribe(ch)
	if _, ok := <-ch; ok {
		t.Fatal("expected channel closed after Unsubscribe")
	}
}
