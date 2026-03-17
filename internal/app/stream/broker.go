package stream

import "sync"

// Broker is a simple pub/sub hub for SSE clients.
// Publish sends a JSON payload to all connected clients.
type Broker struct {
	mu      sync.RWMutex
	clients map[chan []byte]struct{}
}

func NewBroker() *Broker {
	return &Broker{clients: make(map[chan []byte]struct{})}
}

// Subscribe registers a new client channel.
func (b *Broker) Subscribe() chan []byte {
	ch := make(chan []byte, 8)
	b.mu.Lock()
	b.clients[ch] = struct{}{}
	b.mu.Unlock()
	return ch
}

// Unsubscribe removes the client channel and closes it.
func (b *Broker) Unsubscribe(ch chan []byte) {
	b.mu.Lock()
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
