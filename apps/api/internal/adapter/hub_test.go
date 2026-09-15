package adapter

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/port"
)

func TestHubFanoutToAllSubscribers(t *testing.T) {
	h := NewHub(4)
	a, closeA := h.Subscribe()
	b, closeB := h.Subscribe()
	defer closeA()
	defer closeB()

	h.Publish(context.Background(), port.EventAccountUpdated, []byte(`{"id":"a1"}`))

	for name, ch := range map[string]<-chan port.StreamEvent{"a": a, "b": b} {
		select {
		case ev := <-ch:
			if ev.Kind != port.EventAccountUpdated {
				t.Fatalf("%s: kind = %q", name, ev.Kind)
			}
			if got := string(ev.Body); got != `{"id":"a1"}` {
				t.Fatalf("%s: body = %q", name, got)
			}
		case <-time.After(time.Second):
			t.Fatalf("%s: no event", name)
		}
	}
}

func TestHubUnsubscribeStopsDelivery(t *testing.T) {
	h := NewHub(4)
	ch, closeFn := h.Subscribe()
	closeFn()

	// Publishing after unsubscribe must not panic on a closed channel.
	h.Publish(context.Background(), port.EventAccountUpdated, nil)

	select {
	case ev, open := <-ch:
		if open && ev.Kind != "" {
			t.Fatal("received an event after unsubscribe")
		}
	default:
		// A drained closed channel is also an acceptable outcome.
	}
}

func TestHubConcurrentPublish(t *testing.T) {
	h := NewHub(32)
	ch, closeFn := h.Subscribe()
	defer closeFn()

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				h.Publish(context.Background(), port.EventWorkerHealth, []byte(`{}`))
			}
		}()
	}
	wg.Wait()

	// The hub never blocks the publisher; either everything landed or the
	// laggard was dropped. Both are correct, so assert only "no deadlock/panic".
	for i := 0; i < 400; i++ {
		select {
		case <-ch:
		default:
			return
		}
	}
}
