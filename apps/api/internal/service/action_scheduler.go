package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm/apps/api/internal/port"
)

// narrowTarget is the slice of port.ScrapeStore the scheduler needs. Kept
// narrow on purpose (ISP): the scheduler only resolves a target's URL, and a
// full ScrapeStore fake is dozens of methods that say nothing about this loop.
type narrowTarget interface {
	GetTarget(ctx context.Context, id string) (domain.Target, error)
	// GetComment resolves a reply target's author, so the published job can
	// carry the handle the worker must see tagged.
	GetComment(ctx context.Context, id string) (domain.Comment, error)
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
	// texts is the operator-supplied per-account comment store (region-select
	// flow). Optional: when a job has a stored body it is used verbatim instead
	// of composing from the template pool; nil or a miss falls back to the pool.
	texts port.ActionTextStore
	// templates is the pool read used to screen an operator-supplied body
	// against the denylists the pool declares. Optional: when it is nil the
	// screen cannot name a rule to enforce, and composeText falls back to
	// template composition — never to shipping the body unscreened.
	templates port.TemplateStore
	// waker resumes a PAUSED container before a job is queued on it, so the
	// worker exists to drain that queue. Optional: nil assumes it is running.
	waker *Waker
	// pool is the stateless fleet: when set, a job is PUSHED over HTTPS to a
	// leased pool worker instead of being queued on the account's container.
	// nil keeps the legacy Redis pull path byte-for-byte.
	pool *PoolDispatch
	// pending holds the release func of each pushed job, keyed by job id, until
	// its verdict arrives. The leases (account lock + worker slot) must outlive
	// the dispatch but not the job.
	pending sync.Map
}

// ReleaseJob runs and forgets the leases held for a pushed job. JobService calls
// it on every terminal attempt; an id that was never pushed (legacy mode) is a
// no-op.
func (s *ActionScheduler) ReleaseJob(jobID string) {
	if v, ok := s.pending.LoadAndDelete(jobID); ok {
		v.(func())()
	}
}

// ActionSchedulerConfig bounds the loop. Zero values fall back to documented
// defaults so a partial config still runs.
type ActionSchedulerConfig struct {
	// TickBudget caps how many jobs one tick may claim (bounds DB + Redis load).
	TickBudget int
	// Cooldown is the per-(account, target) gate window (P3-09).
	Cooldown time.Duration
	// AccountPace is the minimum spacing between ANY two actions on the same
	// account, regardless of target or action type. The per-target Cooldown does
	// not bound burst velocity across a like+comment+reply on one account within
	// minutes — the exact pattern that gets a fresh account flagged/suspended by
	// Meta. This gate does. Zero disables it (local play); a real deployment
	// spaces actions out (minutes), so an account never machine-guns a platform.
	AccountPace time.Duration
	// RateWindow is the platform budget window (P3-10), an hour by contract.
	RateWindow time.Duration
	// RateLimits is the per-platform hourly cap (IG 30, Threads 15). A platform
	// absent here or set to 0 is disabled, never unlimited.
	RateLimits map[string]int
	// Clock is injectable; defaults to time.Now.
	Clock func() time.Time
	// Waker resumes a PAUSED container before a job is queued on it. Optional.
	Waker *Waker
	// Pool switches the loop to stateless HTTPS push. Optional: nil keeps the
	// Redis pull path.
	Pool *PoolDispatch
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
	texts port.ActionTextStore,
	templates port.TemplateStore,
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
		texts:     texts,
		templates: templates,
		waker:     cfg.Waker,
		pool:      cfg.Pool,
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
	// Requeue jobs whose worker died before reporting back. A claim is the only
	// writer that moves a job to RUNNING and only applyVerdict moves it out, so
	// one lost callback used to strand it in "Verifying" forever — unclaimable
	// (the claim query takes PENDING only) and un-retryable (Retry takes FAILED).
	if n, err := s.actions.SweepStaleRunningJobs(ctx, StaleRunningAfter); err != nil {
		// Never fatal: a sweep failure must not stop the tick from dispatching
		// the jobs that are legitimately due.
		s.log.Warn("action: stale-RUNNING sweep failed", "err", err)
	} else if n > 0 {
		s.log.Info("action: requeued stranded jobs", "count", n)
	}
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
	// Stateless pool: an account is NOT bound to a container. It is idle
	// (worker_id nil) unless an interactive session holds a worker, so the
	// dispatch leases one here. Everything downstream is shared with the legacy
	// path.
	pooled := s.pool != nil
	workerID := ""
	if acc.WorkerID != nil {
		workerID = *acc.WorkerID
	}
	if !pooled {
		// A job with no worker to run it goes back, not to the scrap heap: the
		// fleet is elastic and a worker may appear on the next reconcile.
		if workerID == "" {
			s.reschedule(ctx, job, s.cfg.Cooldown, "no worker assigned to account")
			return
		}
	}

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
	// Record which pool variant produced this body, so the dedupe query can see
	// it as used against this target. Written before the gates: the job row is
	// the durable half of the record, and the worker callback (the other half)
	// can only echo back what it was sent. An operator body leaves it empty —
	// there is no variant to claim.
	if text.templateID != "" {
		job.TemplateID = text.templateID
		if err := s.actions.SetActionJobTemplate(ctx, job.ID, text.templateID); err != nil {
			// Not fatal: the worker still echoes the id back on the callback, so
			// action_log.template_id is still written and the dedupe still works.
			// The job row is the nicer place to read it from, not the only one.
			s.log.Warn("action: record template id on job failed", "job", job.ID, "template", text.templateID, "err", err)
		}
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

	// Gate 3: per-account pacing. Claimed LAST, just before publish, so a job
	// that fails an earlier gate never burns this account's pacing slot. Keyed
	// on the account with a fixed sentinel (not the target), so it fires whether
	// the last action was a like, a comment or a reply — the burst-velocity
	// bound the per-target cooldown cannot give. Refused -> reschedule past the
	// window rather than loop.
	if s.cooldown != nil && s.cfg.AccountPace > 0 {
		ok, err := s.cooldown.Acquire(ctx, job.AccountID, accountPaceKey, s.cfg.AccountPace)
		if err != nil {
			s.reschedule(ctx, job, s.cfg.AccountPace, fmt.Sprintf("account pace gate: %v", err))
			return
		}
		if !ok {
			s.reschedule(ctx, job, s.cfg.AccountPace, "account is pacing (a recent action is still within the per-account window)")
			return
		}
	}

	var publishErr error
	if pooled {
		publishErr = s.publishPooled(ctx, job, acc, tgt, text)
	} else {
		publishErr = s.publish(ctx, job, acc, workerID, tgt, text)
	}
	if publishErr != nil {
		// Transport down: the job was NOT delivered, so release the cooldown
		// slot by simply letting it lapse (TTL is short) and requeue the job.
		s.reschedule(ctx, job, s.cfg.Cooldown, fmt.Sprintf("publish: %v", publishErr))
		return
	}
	s.log.Info("action published", "job", job.ID, "worker", workerID, "attempt", job.Attempts)
}

// composeText resolves the text a comment job will post, along with the pool
// variant it came from (empty for an operator-supplied body), and screens the
// text against the denylist. Returns no text for non-comment actions (a like
// posts nothing) and when no composer is wired (local play / tests).
//
// Both branches are screened. The operator branch used to return the stored
// body verbatim on the strength of a comment claiming it was "screened against
// the denylist" — it was not: MatchBanned lives inside the template pick, so a
// per-account body submitted through POST /api/actions reached a live account
// with nothing but a length check behind it, while the same word inside a
// template was rejected. The screen now runs on the composed text whoever
// wrote it (P3-03).
func (s *ActionScheduler) composeText(ctx context.Context, job domain.ActionJob, platform domain.Platform) (composedText, error) {
	// Both a comment and a reply post text; a like/report posts nothing.
	needsText := job.Type == domain.JobTypeActionComment || job.Type == domain.JobTypeActionReplyComment
	if !needsText {
		return composedText{}, nil
	}
	// An operator-supplied per-account body (region-select flow) wins over the
	// template pool. A store miss or error falls through to composition, so the
	// job still ships a template rather than failing on a transient Redis blip.
	if s.texts != nil {
		if text, ok, err := s.texts.Get(ctx, job.ID); err != nil {
			s.log.Warn("read per-account comment text failed; falling back to template", "job", job.ID, "err", err)
		} else if ok && strings.TrimSpace(text) != "" {
			// The screen is not optional on this branch: the denylists the pool
			// declares are the account's only policy about what may be posted,
			// and an operator body is text posted to a live account like any
			// other. An operator body has no per-template denylist of its own
			// (that is a property of a pool row), so every declared pattern is
			// enforced against it.
			if s.templates == nil {
				// No pool read means no denylist is available here. Posting an
				// unscreened body would re-open this finding, so say so and fall
				// through to the composer, which screens with the variant's own
				// denylist.
				s.log.Warn("per-account comment text not screened: no template store wired; composing instead", "job", job.ID)
			} else {
				patterns, err := s.poolBanned(ctx, platform)
				if err != nil {
					return composedText{}, fmt.Errorf("screen operator text: %w", err)
				}
				if err := screenBanned(text, patterns); err != nil {
					return composedText{}, err
				}
				// No template id: the body came from the operator, not the pool.
				// Recording the variant it most resembles would be a lie about
				// the audit trail, and a wrong id would mark an unused variant
				// as spent for the target under the 7-day dedupe.
				return composedText{text: text}, nil
			}
		}
	}
	if s.composer == nil {
		return composedText{}, nil
	}
	// ponytail: template var values ({topic}, {product}) come from Target.Meta
	// once its shape is pinned; plain-text templates ship now, and a template
	// with an unfilled var is rejected by the engine rather than posted raw.
	out, err := s.composer.Pick(ctx, platform, job.TargetID, nil)
	if err != nil {
		return composedText{}, fmt.Errorf("compose: %w", err)
	}
	return composedText{text: out.RenderedText, templateID: out.TemplateID}, nil
}

// poolBanned collects every denylist pattern the platform's pool declares, so
// an operator-supplied body is held to the same rules a composed one is. The
// pool is a curated set (templates are authored by the operator, not generated
// at volume), so one page of listPageSize covers it; a pool that outgrows the
// page has a pruning problem, not a paging one.
func (s *ActionScheduler) poolBanned(ctx context.Context, platform domain.Platform) ([]string, error) {
	templates, err := s.templates.ListTemplates(ctx, platform, false, &listPageSize, nil)
	if err != nil {
		return nil, fmt.Errorf("list templates: %w", err)
	}
	var banned []string
	for _, t := range templates {
		banned = append(banned, t.BannedWords...)
	}
	return banned, nil
}

// composedText is the result of the compose step: the body a worker will post,
// and the pool variant it came from. TemplateID is empty for a like and for an
// operator-supplied body, and it rides the payload so the worker echoes it back
// on every callback — that echo is the only path by which action_log.template_id
// gets a value, and without it the 7-day per-target dedupe (PickForTarget joins
// action_log on template_id) can never see a variant as used.
type composedText struct {
	text       string
	templateID string
}

// buildJob resolves the target-derived field of a job. A reply must tag the
// comment's author: the handle is read here, from the comment the target points
// at, so the worker can verify IG's pre-filled mention names that person — a
// top-level composer pre-fills the POST author, which a bare "@" check accepts.
// Best-effort: an unresolved handle leaves the field empty and the worker falls
// back to its older, looser check rather than refusing to act.
func (s *ActionScheduler) buildJob(ctx context.Context, job domain.ActionJob, acc domain.Account, tgt domain.Target, text composedText) workerActionJob {
	expectedMention := ""
	if job.Type == domain.JobTypeActionReplyComment && tgt.CommentID != nil {
		if c, err := s.scrapes.GetComment(ctx, *tgt.CommentID); err == nil {
			expectedMention = c.AuthorHandle
		} else {
			s.log.Warn("action: could not resolve reply target author", "job", job.ID, "comment", *tgt.CommentID, "err", err)
		}
	}
	return workerActionJob{
		ID:              job.ID,
		AccountID:       job.AccountID,
		Platform:        string(acc.Platform),
		Action:          actionName(job.Type),
		TargetURL:       tgt.URL,
		Text:            text.text,
		TemplateID:      text.templateID,
		ExpectedMention: expectedMention,
		Attempt:         job.Attempts,
	}
}

// publish serialises the job exactly as the worker's ActionJob expects and
// appends it to that worker's durable queue (FIFO).
func (s *ActionScheduler) publish(ctx context.Context, job domain.ActionJob, acc domain.Account, workerID string, tgt domain.Target, text composedText) error {
	if s.transport == nil {
		return errors.New("transport not configured")
	}
	payload, err := json.Marshal(s.buildJob(ctx, job, acc, tgt, text))
	if err != nil {
		return fmt.Errorf("marshal action job: %w", err)
	}
	// Wake BEFORE enqueueing. The queue is durable, so a container that starts
	// a second later still drains it — but a container that stays PAUSED would
	// leave the job sitting there until the operator noticed, and the job's own
	// backoff would blame the platform for it.
	if s.waker != nil {
		if err := s.waker.Wake(ctx, workerID); err != nil {
			return fmt.Errorf("wake worker: %w", err)
		}
	}
	if err := s.transport.Enqueue(ctx, workerID, payload); err != nil {
		return fmt.Errorf("enqueue: %w", err)
	}
	return nil
}

// publishPooled pushes the same job over HTTPS to a freshly leased pool worker.
// There is no queue and no waker: the request IS the wake.
func (s *ActionScheduler) publishPooled(ctx context.Context, job domain.ActionJob, acc domain.Account, tgt domain.Target, text composedText) error {
	payload := s.buildJob(ctx, job, acc, tgt, text)

	worker, release, err := s.pool.Acquire(ctx, job, acc)
	if err != nil {
		return err
	}
	spec, err := s.pool.RuntimeSpec(ctx, acc)
	if err != nil {
		release()
		return err
	}
	req := port.ExecuteActionRequest{
		JobID:           payload.ID,
		Action:          payload.Action,
		TargetURL:       payload.TargetURL,
		Text:            payload.Text,
		TemplateID:      payload.TemplateID,
		ExpectedMention: payload.ExpectedMention,
		Attempt:         payload.Attempt,
		Runtime:         spec,
	}
	if err := s.pool.Push(ctx, worker, req); err != nil {
		// Nothing reached the worker: free both leases so the next tick can
		// retry against a different machine.
		release()
		return err
	}
	// The verdict arrives by callback; JobService.RecordAttempt releases the
	// leases when it does. Handing the release func over via the job id keeps
	// the callback path free of scheduler state.
	s.pending.Store(job.ID, release)
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
// composed, denylist-screened comment body — the worker never composes or
// screens, only posts what it was given (ADR 0011: policy in the API).
// TemplateID is the pool variant that produced the text, echoed back on the
// callback so action_log records it; empty when the body is operator-supplied.
type workerActionJob struct {
	ID         string `json:"id"`
	AccountID  string `json:"accountId"`
	Platform   string `json:"platform"`
	Action     string `json:"action"`
	TargetURL  string `json:"targetUrl"`
	Text       string `json:"text,omitempty"`
	TemplateID string `json:"templateId,omitempty"`
	// ExpectedMention is the handle a reply must tag, without the @. Set only
	// for reply_comment, from the target comment's author: the worker verifies
	// IG's pre-filled mention names this person, because a top-level composer
	// pre-fills the POST author instead and a bare "@" check would accept it.
	ExpectedMention string `json:"expectedMention,omitempty"`
	Attempt         int    `json:"attempt"`
}

// accountPaceKey is the fixed sentinel "target" the per-account pacing gate
// (Gate 3) claims its slot under. It is not a URL, so it can never collide with
// a real target's cooldown slot; combined with the account id in the gate's
// keyspace it yields one pacing slot per account.
const accountPaceKey = "__account_pace__"

// actionName maps the queue's JobType to the action verb the worker dispatches
// on. The worker's switch is over these strings, so a new action is one case
// here and one case there.
func actionName(t domain.JobType) string {
	switch t {
	case domain.JobTypeActionComment:
		return "comment"
	case domain.JobTypeActionReplyComment:
		return "reply_comment"
	case domain.JobTypeActionLikeComment:
		return "like_comment"
	case domain.JobTypeActionLike:
		return "like"
	default:
		return string(t)
	}
}
