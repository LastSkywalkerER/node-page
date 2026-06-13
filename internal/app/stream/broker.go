package stream

import "sync"

// Broker is a simple pub/sub hub for SSE clients.
// Publish sends a JSON payload to all connected clients.
type Broker struct {
	mu      sync.RWMutex
	clients map[chan []byte]struct{}
	closed  bool
	done    chan struct{}
}

func NewBroker() *Broker {
	return &Broker{clients: make(map[chan []byte]struct{}), done: make(chan struct{})}
}

// Done returns a channel that is closed when CloseAll is called. Handlers that
// are not subscribed to the broker (e.g. the keepalive-only loop for unknown
// hosts) can select on it to exit promptly during graceful shutdown.
func (b *Broker) Done() <-chan struct{} {
	return b.done
}

// Subscribe registers a new client channel. After CloseAll has been called the
// broker is shut down: it returns an already-closed channel so the caller's
// receive loop exits immediately rather than blocking forever.
func (b *Broker) Subscribe() chan []byte {
	ch := make(chan []byte, 8)
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		close(ch)
		return ch
	}
	b.clients[ch] = struct{}{}
	b.mu.Unlock()
	return ch
}

// Unsubscribe removes the client channel and closes it. It is a no-op if the
// channel was already removed/closed by CloseAll, so a handler's deferred
// Unsubscribe after shutdown does not double-close.
func (b *Broker) Unsubscribe(ch chan []byte) {
	b.mu.Lock()
	if _, ok := b.clients[ch]; !ok {
		b.mu.Unlock()
		return
	}
	delete(b.clients, ch)
	b.mu.Unlock()
	close(ch)
}

// Publish sends data to all subscribers, dropping slow clients.
func (b *Broker) Publish(data []byte) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for ch := range b.clients {
		select {
		case ch <- data:
		default:
			// client too slow — skip this message
		}
	}
}

// CloseAll closes every subscriber channel and marks the broker closed, so
// SSE handlers blocked on a receive unblock (ok == false) and exit. Used
// during graceful shutdown. Subsequent Subscribe calls return a closed
// channel; subsequent Unsubscribe calls become no-ops.
func (b *Broker) CloseAll() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	b.closed = true
	close(b.done)
	for ch := range b.clients {
		delete(b.clients, ch)
		close(ch)
	}
}
