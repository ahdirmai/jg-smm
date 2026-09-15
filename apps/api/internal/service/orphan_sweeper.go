package service

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/port"
)

// OrphanSweeper deletes platform resources whose Worker row is gone. The
// reconciler drives desired state for rows that exist; this is the safety net
// for the opposite leak: a pod+PVC+service left behind when a row was deleted
// while the reconciler was down, or a create that raced with a delete.
//
// To avoid deleting a pod that is simply mid-create (row written, reconcile
// loop not yet run), an ID must be observed orphaned for Grace before it is
// deleted (60 second safety net, P1-06).
type OrphanSweeper struct {
	workers port.WorkerStore
	driver  port.K8sClient
	clock   port.Clock
	logger  *slog.Logger
	grace   time.Duration
	// firstSeen records when an ID was first observed orphaned.
	firstSeen map[string]time.Time
}

// SweeperConfig tunes the sweep.
type SweeperConfig struct {
	// Grace is how long an orphan must be observed before deletion.
	Grace  time.Duration
	Clock  port.Clock
	Logger *slog.Logger
}

// NewOrphanSweeper wires the sweeper. Grace <= 0 defaults to 60s.
func NewOrphanSweeper(workers port.WorkerStore, driver port.K8sClient, cfg SweeperConfig) *OrphanSweeper {
	if cfg.Grace <= 0 {
		cfg.Grace = 60 * time.Second
	}
	if cfg.Clock == nil {
		cfg.Clock = systemClock{}
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &OrphanSweeper{
		workers:   workers,
		driver:    driver,
		clock:     cfg.Clock,
		logger:    cfg.Logger,
		grace:     cfg.Grace,
		firstSeen: map[string]time.Time{},
	}
}

// Sweep runs one pass. It returns the IDs it deleted.
func (s *OrphanSweeper) Sweep(ctx context.Context) ([]string, error) {
	running, err := s.driver.ListRunning(ctx)
	if err != nil {
		return nil, fmt.Errorf("sweeper: list running: %w", err)
	}
	if len(running) == 0 {
		// Nothing on the platform: clear the seen map so a later restart does
		// not carry stale timestamps.
		s.firstSeen = map[string]time.Time{}
		return nil, nil
	}

	known, err := s.workers.List(ctx, port.WorkerFilter{Limit: 500})
	if err != nil {
		return nil, fmt.Errorf("sweeper: list workers: %w", err)
	}
	have := make(map[string]struct{}, len(known))
	for _, w := range known {
		have[w.ID] = struct{}{}
	}

	now := s.clock.Now()
	var deleted []string
	for _, id := range running {
		if _, ok := have[id]; ok {
			delete(s.firstSeen, id) // row exists, not an orphan
			continue
		}
		if _, seen := s.firstSeen[id]; !seen {
			s.firstSeen[id] = now
			s.logger.Info("sweeper: orphan observed, waiting out grace",
				"workerId", id, "grace", s.grace.String())
			continue
		}
		if now.Sub(s.firstSeen[id]) < s.grace {
			continue
		}
		if err := s.driver.DeleteWorker(ctx, id); err != nil {
			s.logger.Error("sweeper: delete orphan failed", "workerId", id, "err", err)
			continue
		}
		delete(s.firstSeen, id)
		deleted = append(deleted, id)
		s.logger.Info("sweeper: orphan deleted", "workerId", id)
	}
	return deleted, nil
}

// Run ticks Sweep until ctx is cancelled.
func (s *OrphanSweeper) Run(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		s.logger.Warn("sweeper disabled: interval not set")
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	s.logger.Info("orphan sweeper started",
		"interval", interval.String(), "grace", s.grace.String())
	for {
		select {
		case <-ctx.Done():
			s.logger.Info("orphan sweeper stopped")
			return
		case <-ticker.C:
			if _, err := s.Sweep(ctx); err != nil {
				s.logger.Error("sweeper tick failed", "err", err)
			}
		}
	}
}
