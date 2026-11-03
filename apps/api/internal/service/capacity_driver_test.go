package service

import (
	"context"
	"fmt"
	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
	"sync"
	"sync/atomic"
	"testing"
)

func TestCapacityDriverConcurrentAdmission(t *testing.T) {
	raw := newFakeDriver()
	d := NewCapacityDriver(raw, 5)
	var wg sync.WaitGroup
	var n atomic.Int32
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			w := domain.Worker{ID: fmt.Sprint(i)}
			var err error
			if i%2 == 0 {
				err = d.CreateWorker(context.Background(), w)
			} else {
				err = d.StartWorker(context.Background(), w)
			}
			if err == nil {
				n.Add(1)
			}
		}(i)
	}
	wg.Wait()
	if n.Load() != 5 {
		t.Fatalf("admitted %d, want 5", n.Load())
	}
	ids, _ := raw.ListRunning(context.Background())
	if len(ids) != 5 {
		t.Fatalf("running %d", len(ids))
	}
	if err := d.StartWorker(context.Background(), domain.Worker{ID: ids[0]}); err != nil {
		t.Fatal(err)
	}
}
func TestCapacityDriverFailsClosed(t *testing.T) {
	raw := newFakeDriver()
	raw.failListRunning = true
	d := NewCapacityDriver(raw, 5)
	if d.StartWorker(context.Background(), domain.Worker{ID: "a"}) == nil {
		t.Fatal("expected failure")
	}
	if raw.startCalls != 0 {
		t.Fatal("started despite count failure")
	}
}
