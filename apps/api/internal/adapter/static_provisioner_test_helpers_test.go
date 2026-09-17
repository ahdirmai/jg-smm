package adapter

import (
	"context"
	"sync"
	"time"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm/apps/api/internal/port"
)

// fakeClock returns a fixed advancing time; used by provisioner tests.
type fakeClock struct{ t time.Time }

func (f *fakeClock) Now() time.Time { return f.t }

// memWorkerStore is an in-memory port.WorkerStore for driver tests.
type memWorkerStore struct {
	mu       sync.Mutex
	workers  map[string]domain.Worker
	beats    []domain.Heartbeat
	failNext error
}

func newMemWorkerStore() *memWorkerStore { return &memWorkerStore{workers: map[string]domain.Worker{}} }

func (m *memWorkerStore) GetByID(ctx context.Context, id string) (domain.Worker, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	w, ok := m.workers[id]
	if !ok {
		return domain.Worker{}, domain.ErrNotFound
	}
	return w, nil
}
func (m *memWorkerStore) GetByName(ctx context.Context, name string) (domain.Worker, error) {
	return domain.Worker{}, domain.ErrNotFound
}
func (m *memWorkerStore) GetByContainerID(ctx context.Context, containerID string) (domain.Worker, error) {
	return domain.Worker{}, domain.ErrNotFound
}
func (m *memWorkerStore) Claim(ctx context.Context, claim port.WorkerClaim) (domain.Worker, error) {
	return domain.Worker{}, domain.ErrNotFound
}
func (m *memWorkerStore) List(ctx context.Context, f port.WorkerFilter) ([]domain.Worker, error) {
	return nil, nil
}
func (m *memWorkerStore) Create(ctx context.Context, w domain.Worker) (domain.Worker, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.workers[w.ID] = w
	return w, nil
}
func (m *memWorkerStore) Update(ctx context.Context, w domain.Worker) (domain.Worker, error) {
	return m.Create(ctx, w)
}
func (m *memWorkerStore) Delete(ctx context.Context, id string) error { return nil }
func (m *memWorkerStore) RecordHeartbeat(ctx context.Context, hb domain.Heartbeat, snap port.WorkerSnapshot) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.beats = append(m.beats, hb)
	return nil
}

// memLogStore is an in-memory port.ProvisionLogStore.
type memLogStore struct {
	mu   sync.Mutex
	logs []domain.ProvisionLog
}

func newMemLogStore() *memLogStore { return &memLogStore{} }

func (m *memLogStore) Append(ctx context.Context, l domain.ProvisionLog) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.logs = append(m.logs, l)
	return nil
}
func (m *memLogStore) ListByWorker(ctx context.Context, workerID string, limit int) ([]domain.ProvisionLog, error) {
	return nil, nil
}
