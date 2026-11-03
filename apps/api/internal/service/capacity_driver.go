package service

import (
	"context"
	"fmt"
	"sync"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm/apps/api/internal/port"
)

// CapacityDriver serializes admission across reconciler and interactive wakes.
// One instance must be shared by all provisioning callers in this process.
type CapacityDriver struct {
	port.K8sClient
	mu  sync.Mutex
	max int
}

func NewCapacityDriver(next port.K8sClient, max int) *CapacityDriver {
	return &CapacityDriver{K8sClient: next, max: max}
}

func (d *CapacityDriver) admit(ctx context.Context, w domain.Worker, start func(context.Context, domain.Worker) error) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.max > 0 {
		ids, err := d.K8sClient.ListRunning(ctx)
		if err != nil {
			return fmt.Errorf("capacity: count running workers: %w", err)
		}
		alreadyRunning := false
		for _, id := range ids {
			if id == w.ID {
				alreadyRunning = true
				break
			}
		}
		if !alreadyRunning && len(ids) >= d.max {
			return fmt.Errorf("worker capacity reached (%d active)", d.max)
		}
	}
	return start(ctx, w)
}

func (d *CapacityDriver) CreateWorker(ctx context.Context, w domain.Worker) error {
	return d.admit(ctx, w, d.K8sClient.CreateWorker)
}

func (d *CapacityDriver) StartWorker(ctx context.Context, w domain.Worker) error {
	return d.admit(ctx, w, d.K8sClient.StartWorker)
}
