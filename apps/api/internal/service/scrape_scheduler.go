package service

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"time"

	"log/slog"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm/apps/api/internal/port"
)

// ScrapeScheduler (P2-02) is the tick loop that drains due scrape jobs in FIFO
// order per account, hands each to the Apify runner, then records the outcome.
//
// Design (TICKETS P2-02 AC):
//   - FIFO per account is enforced by the ClaimNextScrapeJob query (ORDER BY
//     scheduled_at FOR UPDATE SKIP LOCKED). One tick never claims the same job
//     twice, and two ticks on different processes never interleave one account.
//   - jitter 5-15s: a claimed job is not run immediately — it is re-scheduled
//     a random 5-15s ahead so a fleet of accounts never bursts at once. This is
//     cheaper than a per-account lock and keeps the bot-detection story honest.
//   - backoff on rate limit: a job whose run reports a rate-limit error is put
//     back with an exponential delay (attempt * base) instead of failing.
//   - one bad account never aborts the loop.
type ScrapeScheduler struct {
	scrapes  port.ScrapeStore
	runner   port.ApifyRunner
	actorFor func(domain.Platform) string
	clock    func() time.Time
	jitter   func() time.Duration
	cfg      ScrapeSchedulerConfig
	log      *slog.Logger
}

// ScrapeSchedulerConfig bounds the loop.
type ScrapeSchedulerConfig struct {
	// JitterMin/Max is the randomized re-schedule window per claimed job.
	JitterMin time.Duration
	JitterMax time.Duration
	// MaxAttempts before a job fails permanently.
	MaxAttempts int
	// BackoffBase is the per-attempt exponential rate-limit delay.
	BackoffBase time.Duration
	// TickBudget caps how many jobs one tick may claim (bounds DB load).
	TickBudget int
	// Clock is injectable; defaults to time.Now.
	Clock func() time.Time
	// Logger defaults to slog.Default.
	Logger *slog.Logger
}

// NewScrapeScheduler wires the loop. The runner is optional: without it the
// scheduler still claims and requeues (useful for smoke-testing the queue), but
// a nil runner is a misconfiguration in every real deployment.
func NewScrapeScheduler(store port.ScrapeStore, runner port.ApifyRunner, cfg ScrapeSchedulerConfig) *ScrapeScheduler {
	return NewScrapeSchedulerWithActor(store, runner, nil, cfg)
}

// NewScrapeSchedulerWithActor is NewScrapeScheduler plus the platform→actor
// mapping; the runner needs it to pick the per-platform payload. nil falls
// back to the runner's own actor resolution.
func NewScrapeSchedulerWithActor(store port.ScrapeStore, runner port.ApifyRunner, actorFor func(domain.Platform) string, cfg ScrapeSchedulerConfig) *ScrapeScheduler {
	if cfg.JitterMin <= 0 {
		cfg.JitterMin = 5 * time.Second
	}
	if cfg.JitterMax < cfg.JitterMin {
		cfg.JitterMax = 15 * time.Second
	}
	if cfg.MaxAttempts < 1 {
		cfg.MaxAttempts = 3
	}
	if cfg.BackoffBase <= 0 {
		cfg.BackoffBase = 2 * time.Minute
	}
	if cfg.TickBudget <= 0 {
		cfg.TickBudget = 20
	}
	if cfg.Clock == nil {
		cfg.Clock = time.Now
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	jitter := func() time.Duration {
		return cfg.JitterMin + time.Duration(rand.Int64N(int64(cfg.JitterMax-cfg.JitterMin)+1))
	}
	return &ScrapeScheduler{
		scrapes:  store,
		runner:   runner,
		actorFor: actorFor,
		clock:    cfg.Clock,
		jitter:   jitter,
		cfg:      cfg,
		log:      cfg.Logger,
	}
}

// Run loops until ctx is cancelled. Every tick claims due jobs across every
// account, so a large fleet is fair without a per-account goroutine.
func (s *ScrapeScheduler) Run(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	s.log.Info("scrape scheduler started", "interval", interval, "tickBudget", s.cfg.TickBudget)
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			s.log.Info("scrape scheduler stopped")
			return
		case <-t.C:
			if err := s.tick(ctx); err != nil {
				// Never let one bad tick kill the loop; the next tick retries.
				s.log.Error("scrape scheduler tick failed", "err", err)
			}
		}
	}
}

// tick claims and runs as many due jobs as the budget allows. It is exported
// for tests so the loop does not have to be time-driven to be exercised.
func (s *ScrapeScheduler) tick(ctx context.Context) error {
	claimed := 0
	for claimed < s.cfg.TickBudget {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		job, err := s.claimAny(ctx)
		if errors.Is(err, domain.ErrNotFound) {
			return nil // nothing due anywhere; empty tick
		}
		if err != nil {
			return err
		}
		claimed++
		s.runOne(ctx, job)
	}
	return nil
}

// claimAny picks one due job regardless of account. Walking account-by-account
// would starve large fleets; a global claim is FIFO within each account and
// fair across them because the SKIP LOCKED rotation is not deterministic.
func (s *ScrapeScheduler) claimAny(ctx context.Context) (domain.ScrapeJob, error) {
	pending, err := s.scrapes.ListScrapeJobsByStatus(ctx, domain.JobStatusPending, &s.cfg.TickBudget, nil)
	if err != nil {
		return domain.ScrapeJob{}, fmt.Errorf("list pending: %w", err)
	}
	now := s.clock()
	for _, job := range pending {
		if !job.ScheduledAt.Before(now) && !job.ScheduledAt.Equal(now) {
			continue // not due yet (jitter window)
		}
		if job.AccountID == nil {
			continue
		}
		claimed, err := s.scrapes.ClaimNextScrapeJob(ctx, *job.AccountID)
		if errors.Is(err, domain.ErrNotFound) {
			continue // lost the race to another tick
		}
		if err != nil {
			return domain.ScrapeJob{}, err
		}
		return claimed, nil
	}
	return domain.ScrapeJob{}, domain.ErrNotFound
}

// runOne executes a claimed job. The outcome paths are all recorded on the job:
// success, a rate-limit requeue with backoff, or a terminal failure.
func (s *ScrapeScheduler) runOne(ctx context.Context, job domain.ScrapeJob) {
	if s.runner == nil {
		// No runner configured: requeue with jitter so the queue keeps moving in
		// smoke tests but never actually scrapes.
		s.requeue(ctx, job, domain.JobStatusPending, "no apify runner configured")
		return
	}

	tgt, err := s.scrapes.GetTarget(ctx, job.TargetID)
	if err != nil {
		s.fail(ctx, job, fmt.Sprintf("target lookup: %v", err))
		return
	}

	out, err := s.runner.Run(ctx, port.ApifyInput{
		ScrapeJobID: job.ID,
		Platform:    tgt.Platform,
		TargetURL:   tgt.URL,
		MaxItems:    100,
	})
	if err != nil {
		// A start failure is transient from the scheduler's point of view: the
		// network blip is not the job's fault, so requeue rather than fail.
		s.requeue(ctx, job, domain.JobStatusRetry, err.Error())
		return
	}
	if isRateLimited(out) {
		s.requeue(ctx, job, domain.JobStatusRetry, out.Error)
		return
	}
	if out.Status != "SUCCEEDED" {
		s.fail(ctx, job, out.Error)
		return
	}
	if _, err := s.scrapes.CompleteScrapeJob(ctx, job.ID, domain.JobStatusSuccess, nil); err != nil {
		s.log.Error("complete scrape job failed", "job", job.ID, "err", err)
	}
}

// requeue puts a job back with a backoff/jitter delay. When attempts exceed the
// cap the job fails permanently rather than looping forever. A requeued job
// returns to PENDING so the next tick can claim it again (RETRY is an
// intermediate state the claim query does not select).
func (s *ScrapeScheduler) requeue(ctx context.Context, job domain.ScrapeJob, status domain.JobStatus, reason string) {
	if job.Attempts >= s.cfg.MaxAttempts {
		s.fail(ctx, job, reason)
		return
	}
	delay := s.jitter()
	if isRateLimitMessage(reason) {
		// Exponential backoff per attempt: attempt * base.
		delay += s.cfg.BackoffBase * time.Duration(job.Attempts)
	}
	next := s.clock().Add(delay)
	if _, err := s.scrapes.CompleteScrapeJob(ctx, job.ID, domain.JobStatusPending, &reason); err != nil {
		// The job stays RUNNING if the write fails; the next tick will not
		// reclaim it, which the operator sees as a stuck job rather than a lost
		// one.
		s.log.Error("requeue scrape job failed", "job", job.ID, "err", err)
		return
	}
	// Reschedule so the jitter window is honoured on the re-claim.
	resched, err := s.scrapes.RescheduleScrapeJob(ctx, job.ID, next)
	if err != nil {
		s.log.Error("reschedule scrape job failed", "job", job.ID, "err", err)
		return
	}
	s.log.Info("scrape job requeued", "job", job.ID, "attempts", resched.Attempts, "delay", delay, "reason", reason)
}

func (s *ScrapeScheduler) fail(ctx context.Context, job domain.ScrapeJob, reason string) {
	reason = truncateErr(reason)
	if _, err := s.scrapes.CompleteScrapeJob(ctx, job.ID, domain.JobStatusFailed, &reason); err != nil {
		s.log.Error("fail scrape job failed", "job", job.ID, "err", err)
	}
	s.log.Warn("scrape job failed", "job", job.ID, "attempts", job.Attempts, "reason", reason)
}

// isRateLimited reports whether the runner hit a rate limit. The runner already
// translated the provider's signal into a status string, so this stays dumb.
func isRateLimited(out port.ApifyOutput) bool {
	return isRateLimitMessage(out.Error)
}

func isRateLimitMessage(s string) bool {
	for _, m := range []string{"rate limit", "429", "RATE_LIMIT"} {
		if contains(s, m) {
			return true
		}
	}
	return false
}

func contains(haystack, needle string) bool {
	return len(needle) > 0 && len(haystack) >= len(needle) &&
		index(haystack, needle) >= 0
}

func index(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}

func truncateErr(s string) string {
	const max = 500
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
