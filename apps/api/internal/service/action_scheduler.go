package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/port"
)

// narrowTarget is the slice of port.ScrapeStore the scheduler needs. Kept
// narrow on purpose (ISP): the scheduler only resolves a target's URL, and a
// full ScrapeStore fake is dozens of methods that say nothing about this loop.
type narrowTarget interface {
	GetTarget(ctx context.Context, id string) (domain.Target, error)
}

// narrowAccount is the slice of port.AccountStore the scheduler needs: the
// worker an account is bound to, and its platform.
type narrowAccount interface {
	GetByID(ctx context.Context, id string) (domain.Account, error)
}

// narrowComposer is the slice of the template engine the scheduler needs
// (P3-03): compose a denylist-screened comment for a target, or explain why
// none may ship. Kept narrow so the loop is testable with a stub, and so a
// future composer change does not ripple into the scheduler signature.
type narrowComposer interface {
	Pick(ctx context.Context, platform domain.Platform, targetID string, values map[string]string) (PickOutcome, error)
}

// ActionScheduler (P3-07/P3-10) is the API-side loop that drains the action
// queue: it claims due action jobs, enforces the two safety gates, and
// publishes each survivor to the worker that owns the account.
//
// Layering (why this is in the API, not the worker):
//   - the worker never touches the DB (ADR 0011) and never decides policy. It
//     only BLPOP a job, run it, and POST the verdict back. Every gate below is
//     a platform/account safety policy, so it belongs in the single writer.
//   - cooldown (P3-09): one (account, target) pair cannot act twice inside 60s.
//   - rate limit (P3-10): a platform's hourly budget is spent before publish,
//     not discovered by the worker being blocked.
//
// Failure modes are all requeue-or-skip, never drop: a job whose gate fails is
// rescheduled with backoff so it gets another tick, and a job whose worker is
// gone is put back rather than lost.
type ActionScheduler struct {
	actions   port.ActionStore
	accounts  narrowAccount
	scrapes   narrowTarget
	composer  narrowComposer
	cooldown  port.CooldownGate
	limits    port.RateLimiter
	transport port.Publisher
	cfg       ActionSchedulerConfig
	clock     func() time.Time
	log       *slog.Logger
}

// ActionSchedulerConfig bounds the loop. Zero values fall back to documented
// defaults so a partial config still runs.
type ActionSchedulerConfig struct {
	// TickBudget caps how many jobs one tick may claim (bounds DB + Redis load).
	TickBudget int
	// Cooldown is the per-(account, target) gate window (P3-09).
	Cooldown time.Duration
	// RateWindow is the platform budget window (P3-10), an hour by contract.
	RateWindow time.Duration
	// RateLimits is the per-platform hourly cap (IG 30, Threads 15). A platform
	// absent here or set to 0 is disabled, never unlimited.
	RateLimits map[string]int
	// Clock is injectable; defaults to time.Now.
	Clock func() time.Time
	// Logger defaults to slog.Default.
	Logger *slog.Logger
}

// NewActionScheduler wires the loop. composer/cooldown/limits/transport may be
// nil: the scheduler then skips those steps (local play) rather than panic, and
// every job still flows to the worker. A nil composer means comment text is not
// composed — the job publishes with no text and the worker rejects it, so a
// real deployment always sets one.
func NewActionScheduler(
	actions port.ActionStore,
	accounts narrowAccount,
	scrapes narrowTarget,
	composer narrowComposer,
	cooldown port.CooldownGate,
	limits port.RateLimiter,
	transport port.Publisher,
	cfg ActionSchedulerConfig,
) *ActionScheduler {
	if cfg.TickBudget <= 0 {
		cfg.TickBudget = 20
	}
	if cfg.Cooldown <= 0 {
		cfg.Cooldown = 60 * time.Second
	}
	if cfg.RateWindow <= 0 {
		cfg.RateWindow = time.Hour
	}
	if cfg.Clock == nil {
		cfg.Clock = time.Now
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &ActionScheduler{
		actions:   actions,
		accounts:  accounts,
		scrapes:   scrapes,
		composer:  composer,
		cooldown:  cooldown,
		limits:    limits,
		transport: transport,
		cfg:       cfg,
		clock:     cfg.Clock,
		log:       cfg.Logger,
	}
}

// Run loops until ctx is cancelled. One tick claims due jobs across every
// account, so a large fleet is fair without a per-account goroutine.
func (s *ActionScheduler) Run(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 20 * time.Second
	}
	s.log.Info("action scheduler started", "interval", interval, "tickBudget", s.cfg.TickBudget)
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			s.log.Info("action scheduler stopped")
			return
		case <-t.C:
			if err := s.Tick(ctx); err != nil {
				// One bad tick never kills the loop; the next tick retries.
				s.log.Error("action scheduler tick failed", "err", err)
			}
		}
	}
}

// Tick claims and dispatches as many due jobs as the budget allows. Exported so
// tests exercise the policy without waiting on the ticker.
func (s *ActionScheduler) Tick(ctx context.Context) error {
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
		s.dispatchOne(ctx, job)
	}
	return nil
}

// claimAny picks one due job regardless of account. Walking account-by-account
// would starve large fleets; a global scan with a per-account claim is FIFO
// within each account and fair across them.
func (s *ActionScheduler) claimAny(ctx context.Context) (domain.ActionJob, error) {
	pending, err := s.actions.ListActionJobsByStatus(ctx, domain.JobStatusPending, &s.cfg.TickBudget, nil)
	if err != nil {
		return domain.ActionJob{}, fmt.Errorf("list pending actions: %w", err)
	}
	now := s.clock()
	for _, job := range pending {
		if !job.ScheduledAt.Before(now) {
			continue // not due yet (backoff/jitter window)
		}
		claimed, err := s.actions.ClaimNextActionJob(ctx, job.AccountID, job.WorkerID)
		if errors.Is(err, domain.ErrNotFound) {
			continue // lost the race to another tick
		}
		if err != nil {
			return domain.ActionJob{}, err
		}
		return claimed, nil
	}
	return domain.ActionJob{}, domain.ErrNotFound
}

// dispatchOne runs a claimed job through the gates and onto the worker. Every
// branch records its decision on the job so the queue is always inspectable.
func (s *ActionScheduler) dispatchOne(ctx context.Context, job domain.ActionJob) {
	tgt, err := s.scrapes.GetTarget(ctx, job.TargetID)
	if err != nil {
		s.fail(ctx, job, fmt.Sprintf("target lookup: %v", err))
		return
	}
	acc, err := s.accounts.GetByID(ctx, job.AccountID)
	if err != nil {
		s.fail(ctx, job, fmt.Sprintf("account lookup: %v", err))
		return
	}
	// A job with no worker to run it goes back, not to the scrap heap: the
	// fleet is elastic and a worker may appear on the next reconcile.
	if acc.WorkerID == nil || *acc.WorkerID == "" {
		s.reschedule(ctx, job, s.cfg.Cooldown, "no worker assigned to account")
		return
	}
	workerID := *acc.WorkerID

	// Compose the comment text before any gate is spent (P3-03): a banned or
	// unrenderable comment cannot ship no matter how much budget it burns, so
	// screening it first is what "ditolak sebelum queue" means. A like needs no
	// text. Both rejections are terminal, not reschedules: re-running the
	// compose would draw the same banned text or the same empty pool, so a loop
	// would only hide the problem while looking busy.
	text, err := s.composeText(ctx, job, acc.Platform)
	if err != nil {
		s.fail(ctx, job, truncateErr(err.Error()))
		return
	}

	// Gate 1: cooldown. A cooled-down target means we already acted on it
	// recently; skipping now is the whole point of the gate.
	if s.cooldown != nil {
		ok, err := s.cooldown.Acquire(ctx, job.AccountID, tgt.URL, s.cfg.Cooldown)
		if err != nil {
			s.reschedule(ctx, job, s.cfg.Cooldown, fmt.Sprintf("cooldown gate: %v", err))
			return
		}
		if !ok {
			s.reschedule(ctx, job, s.cfg.Cooldown, "target is cooling down")
			return
		}
	}

	// Gate 2: the platform's hourly budget. Refused -> reschedule for the reset
	// so the job does not loop tightly against a spent quota.
	if s.limits != nil {
		limit := s.cfg.RateLimits[string(acc.Platform)]
		ok, retryAfter, err := s.limits.Allow(ctx, string(acc.Platform), limit, s.cfg.RateWindow)
		if err != nil {
			s.reschedule(ctx, job, s.cfg.Cooldown, fmt.Sprintf("rate limiter: %v", err))
			return
		}
		if !ok {
			delay := retryAfter
			if delay <= 0 {
				delay = s.cfg.RateWindow
			}
			s.reschedule(ctx, job, delay, "platform rate budget spent")
			return
		}
	}

	if err := s.publish(ctx, job, acc, workerID, tgt.URL, text); err != nil {
		// Transport down: the job was NOT delivered, so release the cooldown
		// slot by simply letting it lapse (TTL is short) and requeue the job.
		s.reschedule(ctx, job, s.cfg.Cooldown, fmt.Sprintf("publish: %v", err))
		return
	}
	s.log.Info("action published", "job", job.ID, "worker", workerID, "attempt", job.Attempts)
}

// composeText resolves the text a comment job will post, screened against the
// denylist. Returns empty text for non-comment actions (a like posts nothing)
// and when no composer is wired (local play / tests).
func (s *ActionScheduler) composeText(ctx context.Context, job domain.ActionJob, platform domain.Platform) (string, error) {
	if s.composer == nil || job.Type != domain.JobTypeActionComment {
		return "", nil
	}
	// ponytail: template var values ({topic}, {product}) come from Target.Meta
	// once its shape is pinned; plain-text templates ship now, and a template
	// with an unfilled var is rejected by the engine rather than posted raw.
	out, err := s.composer.Pick(ctx, platform, job.TargetID, nil)
	if err != nil {
		return "", fmt.Errorf("compose: %w", err)
	}
	return out.RenderedText, nil
}

// publish serialises the job exactly as the worker's ActionJob expects and
// appends it to that worker's durable queue (FIFO).
func (s *ActionScheduler) publish(ctx context.Context, job domain.ActionJob, acc domain.Account, workerID, targetURL, text string) error {
	if s.transport == nil {
		return errors.New("transport not configured")
	}
	payload, err := json.Marshal(workerActionJob{
		ID:        job.ID,
		AccountID: job.AccountID,
		Platform:  string(acc.Platform),
		Action:    actionName(job.Type),
		TargetURL: targetURL,
		Text:      text,
		Attempt:   job.Attempts,
	})
	if err != nil {
		return fmt.Errorf("marshal action job: %w", err)
	}
	if err := s.transport.Enqueue(ctx, workerID, payload); err != nil {
		return fmt.Errorf("enqueue: %w", err)
	}
	return nil
}

// reschedule returns a job to the queue after a delay. The delay is the gate's
// honest answer ("try again in N"), not a fixed constant, so a spent budget is
// respected rather than polled.
func (s *ActionScheduler) reschedule(ctx context.Context, job domain.ActionJob, delay time.Duration, reason string) {
	at := s.clock().Add(delay)
	if _, err := s.actions.RescheduleActionJob(ctx, job.ID, at); err != nil {
		s.log.Error("reschedule action failed", "job", job.ID, "err", err)
		return
	}
	s.log.Info("action rescheduled", "job", job.ID, "delay", delay, "reason", reason)
}

func (s *ActionScheduler) fail(ctx context.Context, job domain.ActionJob, reason string) {
	reason = truncateErr(reason)
	if _, err := s.actions.CompleteActionJob(ctx, job.ID, domain.JobStatusFailed, &reason); err != nil {
		s.log.Error("fail action failed", "job", job.ID, "err", err)
	}
	s.log.Warn("action failed to dispatch", "job", job.ID, "reason", reason)
}

// workerActionJob is the wire shape the worker BLPOPs. Kept here so the
// scheduler is the only place that decides what a worker sees. Text is the
// already-composed, denylist-screened comment body; the worker never composes
// or screens, only posts what it was given (ADR 0011: policy in the API).
type workerActionJob struct {
	ID        string `json:"id"`
	AccountID string `json:"accountId"`
	Platform  string `json:"platform"`
	Action    string `json:"action"`
	TargetURL string `json:"targetUrl"`
	Text      string `json:"text,omitempty"`
	Attempt   int    `json:"attempt"`
}

// actionName maps the queue's JobType to the action verb the worker dispatches
// on. The worker's switch is over these strings, so a new action is one case
// here and one case there.
func actionName(t domain.JobType) string {
	switch t {
	case domain.JobTypeActionComment:
		return "comment"
	case domain.JobTypeActionLike:
		return "like"
	default:
		return string(t)
	}
}
