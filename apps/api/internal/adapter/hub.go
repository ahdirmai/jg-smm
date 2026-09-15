package adapter

import (
	"context"
	"sync"

	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/port"
)

// Hub is the in-process SSE fan-out. Each connected dashboard holds one
// Subscriber; Publish copies the event to every queue. It is deliberately not
// durable and not cross-node: SSE clients are pinned to the instance that
// served them, which is correct for a single-node MVP. When the API is scaled
// horizontally, a Redis pub/sub backing this hub is the upgrade path — the
// port interface hides it from callers.
type Hub struct {
	mu     sync.RWMutex
	subs   map[chan port.StreamEvent]struct{}
	buffer int
}

// NewHub builds a hub. buffer is the per-subscriber queue; a slow consumer that
// overflows it is dropped (its queue is closed) rather than blocking the hub.
func NewHub(buffer int) *Hub {
	if buffer < 1 {
		buffer = 16
	}
	return &Hub{
		subs:   map[chan port.StreamEvent]struct{}{},
		buffer: buffer,
	}
}

var _ port.StreamPublisher = (*Hub)(nil)

// Subscribe returns a channel of events plus a closer. The caller MUST drain or
// the queue is closed on overflow; a closed queue is a signal to reconnect.
func (h *Hub) Subscribe() (<-chan port.StreamEvent, func()) {
	ch := make(chan port.StreamEvent, h.buffer)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		if _, ok := h.subs[ch]; ok {
			delete(h.subs, ch)
			close(ch)
		}
		h.mu.Unlock()
	}
}

// Publish fans out to every subscriber without blocking the caller. A full
// queue means the consumer is too slow; it is dropped so the writer (the API)
// never stalls on a dashboard tab someone left open.
func (h *Hub) Publish(_ context.Context, kind string, payload []byte) {
	ev := port.StreamEvent{Kind: kind, Body: payload}

	h.mu.RLock()
	var dead []chan port.StreamEvent
	for ch := range h.subs {
		select {
		case ch <- ev:
		default:
			// Overflow: drop and disconnect the laggard. Its handler sees a
			// closed channel and the browser's EventSource reconnects.
			dead = append(dead, ch)
		}
	}
	h.mu.RUnlock()

	if len(dead) > 0 {
		h.mu.Lock()
		for _, ch := range dead {
			if _, ok := h.subs[ch]; ok {
				delete(h.subs, ch)
				close(ch)
			}
		}
		h.mu.Unlock()
	}
}
