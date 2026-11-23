package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm/apps/api/internal/port"
)

// MaxActionBatch is the enqueue cap (P3-13 "queue 50"): one request may carry
// up to this many intents, and the queue page shows the same window.
const MaxActionBatch = 50

// Runner text is one line by construction: the worker types it with
// pressSequentially, and a newline there is an Enter keypress — the comment is
// submitted early, the composer clears, and the remainder is typed into the
// fresh composer and abandoned. The reply then reports SUCCESS with the full
// two-line string as renderedText, a string that was never posted. Rejecting it
// is the honest answer: silently truncating would post a different comment than
// the operator wrote, and accepting it would post a fragment of one.
const newlineChars = "\n\r"

// StaleRunningAfter is how long a job may sit in RUNNING before the scheduler
// requeues it. A healthy action finishes in seconds; 15 minutes clears any
// real slow render while still bounding the damage from a dead worker.
const StaleRunningAfter = 15 * time.Minute

// ActionService (P3-13) is the operator-facing side of the action engine: it
// turns a paste of target URLs into PENDING jobs, and it reads the queue back
// with the verdict that explains each row. It deliberately holds no action
// policy — the cooldown, rate budget and denylist screening live in the
// scheduler and the template engine, not here (ADR 0011: policy in the API,
// but enqueue is intent, not policy).
type ActionService struct {
	actions  port.ActionStore
	accounts narrowAccount
	scrapes  narrowTargetWriter
	clock    port.Clock
	log      *slog.Logger
	// texts holds an operator-supplied per-account comment body until the
	// scheduler composes the job. Optional: nil means the region-select flow's
	// custom comment is unavailable and every comment falls back to the template
	// pool (local play / no Redis).
	texts port.ActionTextStore
	// batches records each enqueue call as a unit for monitoring. Optional and
	// best-effort: nil (or a store failure) never blocks an enqueue.
	batches port.ActionBatchStore
	// stream announces newly enqueued jobs to open dashboards. Optional: nil
	// means "no dashboard wired" and must never fail the write.
	//
	// Without this, a job an operator just queued is invisible to the queue page
	// until a *worker* reports back on it — the page's only other refresh trigger
	// is the action-updated frame the job service publishes on a verdict. With
	// no worker running (or one that has not yet claimed the job) the row simply
	// never appears, which reads as a silently failed enqueue.
	stream port.StreamPublisher
}

// ActionTextTTL bounds how long a per-account comment body waits for dispatch.
// Generous relative to the pacing gate (minutes) so a queued custom comment is
// never dropped before its job runs, but not unbounded.
const ActionTextTTL = 24 * 60 * 60 // seconds (24h)

// narrowTargetWriter is the slice of port.ScrapeStore the enqueue path needs:
// turn a pasted permalink into a Target row (find-or-create) so the scheduler
// can resolve it back to a URL at dispatch.
//
// The comment methods exist so a REPLY target can be linked to the comment it
// answers. Without that link the dispatch-side author lookup has nothing to
// resolve, and the worker's tag check degrades to "a mention exists" — which a
// wrong tag passes.
type narrowTargetWriter interface {
	UpsertTarget(ctx context.Context, t domain.Target) (domain.Target, error)
	GetCommentByExternalID(ctx context.Context, p domain.Platform, externalID string) (domain.Comment, error)
	LinkTargetComment(ctx context.Context, targetID, commentID string) error
}

// targetReader is an optional read capability on narrowTargetWriter (satisfied
// by ScrapeRepo in production) so List can resolve each unique targetId to its
// URL for the queue UI without forcing minimal test fakes to implement it.
type targetReader interface {
	GetTarget(ctx context.Context, id string) (domain.Target, error)
}

// ActionItem is one intent from the enqueue request.
type ActionItem struct {
	AccountID string
	TargetURL string
	Type      domain.JobType
	// Text is an optional operator-supplied comment body (region-select flow's
	// per-account comment). Empty → the scheduler composes from the template
	// pool as before. Only meaningful for comment/reply actions; ignored for a
	// like. Held in the text store keyed by the created job id, read back at
	// dispatch. Requires a text store to be wired — without one it is dropped
	// (and the job falls back to a template).
	Text string
}

// MaxCommentTextLen caps an operator-supplied comment body. Instagram's own
// limit is ~2200 chars; this rejects a pathologically long paste before it
// reaches the store or the worker.
const MaxCommentTextLen = 2200

// MaxAICommentVariants caps how many distinct AI drafts one generate call
// returns. The dashboard requests one per selected account and its batch cap
// is 50, so this is sized to match rather than truncate silently.
const MaxAICommentVariants = 50

// ActionView is one queue row: the job's live status plus the latest attempt's
// verdict, which is what an operator actually reads.
type ActionView struct {
	Job          domain.ActionJob
	TargetURL    string
	RenderedText string
	ErrorClass   string
}

// NewActionService wires the service. A nil clock defaults to the system clock.
// A nil stream disables enqueue announcements (the write still succeeds).
func NewActionService(
	actions port.ActionStore,
	accounts narrowAccount,
	scrapes narrowTargetWriter,
	clock port.Clock,
	logger *slog.Logger,
	texts port.ActionTextStore,
	batches port.ActionBatchStore,
	stream port.StreamPublisher,
) *ActionService {
	if logger == nil {
		logger = slog.Default()
	}
	if clock == nil {
		clock = systemClock{}
	}
	return &ActionService{actions: actions, accounts: accounts, scrapes: scrapes, clock: clock, log: logger, texts: texts, batches: batches, stream: stream}
}

// Enqueue turns a batch of intents into PENDING jobs. Every item is validated
// before any row is written: a batch is all-or-nothing, because a partial
// enqueue that silently drops the bad rows is how an operator loses trust in
// the queue. Comment text is never accepted here — it is composed from the
// template pool and denylist-screened at dispatch (P3-02/P3-03).
func (s *ActionService) Enqueue(ctx context.Context, items []ActionItem) ([]domain.ActionJob, error) {
	if len(items) == 0 {
		return nil, fmt.Errorf("%w: enqueue must carry at least one action", domain.ErrValidation)
	}
	if len(items) > MaxActionBatch {
		return nil, fmt.Errorf("%w: enqueue carries %d actions, the cap is %d", domain.ErrValidation, len(items), MaxActionBatch)
	}

	platforms := make(map[string]domain.Platform, len(items))
	jobs := make([]domain.ActionJob, 0, len(items))
	now := s.clock.Now()

	// Per-item comment bodies are stored AFTER their jobs are created (a job id
	// is the key), so remember each item's text by its index in the batch.
	texts := make(map[int]string, len(items))

	for i, item := range items {
		if err := validateActionType(item.Type); err != nil {
			return nil, fmt.Errorf("item %d: %w", i, err)
		}
		// The account decides the platform, and the target must live on that
		// platform's own host — so the account is resolved before the URL is
		// validated, not after.
		acc, err := s.accounts.GetByID(ctx, item.AccountID)
		if err != nil {
			return nil, fmt.Errorf("item %d: account lookup: %w", i, err)
		}
		if !acc.IsPackable() {
			return nil, fmt.Errorf("item %d: %w: account %s is %s", i, domain.ErrValidation, acc.ID, acc.Status)
		}
		// An action runs a real browser as this account, so a session that
		// never completed a login cannot act: the worker would open Instagram
		// logged out and the job would fail after a paid dispatch. Refuse at
		// enqueue with the reason the operator can fix on the Accounts page.
		if acc.AuthStatus != domain.AuthAuthenticated {
			return nil, fmt.Errorf("item %d: %w: @%s is %s — log in on the Accounts page before acting as it",
				i, domain.ErrValidation, acc.Username, acc.AuthStatus)
		}
		platforms[item.AccountID] = acc.Platform

		u, err := safeTargetURL(item.TargetURL, acc.Platform)
		if err != nil {
			return nil, fmt.Errorf("item %d: %w", i, err)
		}
		// A reply must name the comment it answers. A bare post permalink used to
		// pass here, and the worker then replied to whichever comment owned the
		// first Reply button on the page.
		//
		// The shape is platform-specific: Instagram addresses a comment as
		// `/p/<post>/c/<id>/`, while Threads has no such route (it 404s) — a
		// Threads comment IS a post with its own shortcode, so its permalink is
		// `/post/<comment code>` (see commentPermalink).
		if item.Type == domain.JobTypeActionReplyComment && replyTargetID(acc.Platform, u.Path) == "" {
			return nil, fmt.Errorf("%d: %w: a reply target must be a comment permalink (%s), got %q", i, domain.ErrValidation, replyPermalinkShape(acc.Platform), u.Path)
		}
		if text := strings.TrimSpace(item.Text); text != "" {
			if len(text) > MaxCommentTextLen {
				return nil, fmt.Errorf("item %d: %w: comment text is %d chars, the cap is %d", i, domain.ErrValidation, len(text), MaxCommentTextLen)
			}
			// One line or nothing: the worker types the body into a composer
			// where Enter submits (see newlineChars).
			if strings.ContainsAny(text, newlineChars) {
				return nil, fmt.Errorf("item %d: %w: comment text must be a single line (no newline or carriage return); a newline submits the comment early and abandons the rest", i, domain.ErrValidation)
			}
			// A supplied body only makes sense for an action that posts text.
			if item.Type != domain.JobTypeActionComment && item.Type != domain.JobTypeActionReplyComment {
				return nil, fmt.Errorf("item %d: %w: comment text is only valid for a comment or reply action, not %s", i, domain.ErrValidation, item.Type)
			}
			if s.texts == nil {
				return nil, fmt.Errorf("item %d: %w: per-account comment text is not available (no text store configured)", i, domain.ErrValidation)
			}
			texts[i] = text
		}
		tgt, err := s.scrapes.UpsertTarget(ctx, domain.Target{
			Kind:       domain.TargetKindPost,
			Platform:   acc.Platform,
			ExternalID: u.String(),
			URL:        u.String(),
		})
		if err != nil {
			return nil, fmt.Errorf("item %d: target upsert: %w", i, err)
		}

		// A reply target must LINK to the comment it answers. Without this the
		// target's comment_id stays NULL and the dispatch-side handle lookup
		// (which sends the worker the author to verify the tag against) can
		// never resolve — the reply then falls back to a presence-only mention
		// check, which a WRONG tag passes.
		//
		// The link is best-effort: a comment we have not scraped is legitimately
		// unknown at enqueue time (the operator can paste any permalink), and a
		// missing link degrades the tag check rather than blocking the job.
		if item.Type == domain.JobTypeActionReplyComment {
			if want := replyTargetID(acc.Platform, u.Path); want != "" {
				if c, err := s.scrapes.GetCommentByExternalID(ctx, acc.Platform, want); err == nil {
					if err := s.scrapes.LinkTargetComment(ctx, tgt.ID, c.ID); err != nil {
						s.log.Warn("reply target: could not link comment", "target", tgt.ID, "err", err)
					}
				} else {
					s.log.Info("reply target: comment not scraped yet, tag check will be presence-only",
						"comment", want, "err", err)
				}
			}
		}

		jobs = append(jobs, domain.ActionJob{
			Type:        item.Type,
			TargetID:    tgt.ID,
			AccountID:   acc.ID,
			WorkerID:    derefStrPtr(acc.WorkerID),
			Status:      domain.JobStatusPending,
			ScheduledAt: now,
		})
	}

	created := make([]domain.ActionJob, 0, len(jobs))
	for idx, j := range jobs {
		row, err := s.actions.CreateActionJob(ctx, j)
		if err != nil {
			return nil, fmt.Errorf("create action job: %w", err)
		}
		// Stash the operator's comment body under the new job id so the scheduler
		// reads it at dispatch instead of composing from templates. Best-effort:
		// a store failure is logged, not fatal — the job still runs and simply
		// falls back to a template, which is the pre-existing behaviour.
		if text, ok := texts[idx]; ok && s.texts != nil {
			if err := s.texts.Put(ctx, row.ID, text, ActionTextTTL*time.Second); err != nil {
				s.log.Warn("store per-account comment text failed; job will fall back to template", "job", row.ID, "err", err)
			}
		}
		created = append(created, row)
	}

	// Record the batch as a monitoring unit so a fan-out (region-select flow) is
	// traceable together, not just as N unrelated jobs. Best-effort: a store
	// failure is logged, never fatal — the jobs are already enqueued and visible
	// per-job. The per-job structured logs also carry the batch id for log
	// correlation.
	batchID := uuid.NewString()
	if s.batches != nil && len(created) > 0 {
		typeSet := map[string]struct{}{}
		urlSet := map[string]struct{}{}
		jobIDs := make([]string, 0, len(created))
		types := make([]string, 0, 4)
		urls := make([]string, 0, len(created))
		for i, j := range created {
			jobIDs = append(jobIDs, j.ID)
			if _, ok := typeSet[string(j.Type)]; !ok {
				typeSet[string(j.Type)] = struct{}{}
				types = append(types, string(j.Type))
			}
			// items[i] is the source of created[i] (same order); record distinct
			// targets, capped so a 50-item batch record stays small.
			if u := items[i].TargetURL; u != "" {
				if _, ok := urlSet[u]; !ok && len(urls) < 20 {
					urlSet[u] = struct{}{}
					urls = append(urls, u)
				}
			}
		}
		batch := port.ActionBatch{
			ID:          batchID,
			CreatedAt:   now,
			ActionTypes: types,
			Count:       len(created),
			TargetURLs:  urls,
			JobIDs:      jobIDs,
		}
		if err := s.batches.Create(ctx, batch); err != nil {
			s.log.Warn("record action batch failed (monitoring only)", "batch", batchID, "err", err)
		}
	}
	s.log.Info("actions enqueued", "batch", batchID, "count", len(created))
	s.publishEnqueued(ctx, created)
	return created, nil
}

// publishEnqueued announces freshly created jobs so an open queue page picks
// them up immediately instead of waiting for a worker verdict that may never
// come (no fleet running, or a job nobody claims).
//
// One frame per job, matching the shape JobService.publishVerdict uses, because
// the browser handler (use-actions.ts) treats every action-updated frame the
// same way: it refetches the list rather than patching from the payload (ADR
// 0010 — the frame is a signal, the read is authoritative). So the payload only
// needs the id; the extra fields keep the two publishers' frames indistinguishable
// to anything that ever does read them.
//
// Fire-and-forget per the port contract: a nil stream or a marshal failure is
// logged, never propagated. The jobs are already persisted at this point, and
// the page still converges on its next fetch.
func (s *ActionService) publishEnqueued(ctx context.Context, jobs []domain.ActionJob) {
	if s.stream == nil {
		return
	}
	for _, j := range jobs {
		body, err := json.Marshal(map[string]any{
			"jobId":   j.ID,
			"status":  string(j.Status),
			"attempt": j.Attempts,
			// Not a verdict: no verified flag, no error class. A consumer that
			// needs to tell "queued" from "finished" has the status for it.
			"enqueued": true,
		})
		if err != nil {
			s.log.Warn("stream: marshal enqueue frame", "job", j.ID, "err", err)
			continue
		}
		s.stream.Publish(ctx, port.EventActionUpdated, body)
	}
}

// ListBatches returns recent enqueue batches (monitoring), newest first. Nil
// batch store (no Redis) yields an empty list, never an error.
func (s *ActionService) ListBatches(ctx context.Context, limit int) ([]port.ActionBatch, error) {
	if s.batches == nil {
		return nil, nil
	}
	return s.batches.List(ctx, limit)
}

// BatchStatus is a batch plus a live rollup of its jobs' current statuses, the
// unit an operator monitors after a fan-out.
type BatchStatus struct {
	Batch    port.ActionBatch         `json:"batch"`
	ByStatus map[domain.JobStatus]int `json:"byStatus"`
	Missing  int                      `json:"missing"` // job ids no longer found (e.g. purged)
}

// GetBatchStatus fetches a batch and rolls up its jobs' current statuses by
// reading each job. Bounded by the batch size (<=50). Returns ErrNotFound when
// the batch id is unknown (or no batch store is wired).
func (s *ActionService) GetBatchStatus(ctx context.Context, id string) (BatchStatus, error) {
	if s.batches == nil {
		return BatchStatus{}, domain.ErrNotFound
	}
	b, ok, err := s.batches.Get(ctx, id)
	if err != nil {
		return BatchStatus{}, fmt.Errorf("get batch: %w", err)
	}
	if !ok {
		return BatchStatus{}, domain.ErrNotFound
	}
	byStatus := map[domain.JobStatus]int{}
	missing := 0
	for _, jobID := range b.JobIDs {
		job, err := s.actions.GetActionJob(ctx, jobID)
		if err != nil {
			missing++
			continue
		}
		byStatus[job.Status]++
	}
	return BatchStatus{Batch: b, ByStatus: byStatus, Missing: missing}, nil
}

// Retry puts a failed job back in the queue for another attempt. Only a
// terminal FAILED job is retryable: re-queueing a PENDING/RUNNING one would
// hand the same work to a second worker, and re-queueing a SUCCESS would repeat
// an action the platform already performed (a second like is a no-op, a second
// comment is a duplicate post).
//
// attempts is deliberately NOT reset — the retry budget is the job's history,
// and the escalation in the attempt log is what tells an operator how many
// times this has already failed.
func (s *ActionService) Retry(ctx context.Context, jobID string) (domain.ActionJob, error) {
	job, err := s.actions.GetActionJob(ctx, jobID)
	if err != nil {
		return domain.ActionJob{}, fmt.Errorf("retry action job: %w", err)
	}
	if job.Status != domain.JobStatusFailed {
		return domain.ActionJob{}, fmt.Errorf("%w: only a failed job can be retried, %s is %s", domain.ErrValidation, jobID, job.Status)
	}
	// Keep the recorded variant across the retry. The id is written at dispatch
	// and the queue UI (its report card) clears the column on every requeue; a
	// cleared id would make the retried job look just like a fresh one, and
	// PickForTarget — which joins action_log on template_id — would happily
	// re-hand the same variant to the retry the dedupe exists to prevent.
	var templateID *string
	if job.TemplateID != "" {
		templateID = &job.TemplateID
	}
	// Now, not now+backoff: the operator pressed the button because they want
	// it to run, and the per-account velocity gate in the scheduler is the
	// thing that should decide whether it is safe yet.
	rescheduled, err := s.actions.RequeueFailedActionJob(ctx, jobID, s.clock.Now())
	if err != nil {
		return domain.ActionJob{}, err
	}
	if templateID != nil {
		if err := s.actions.SetActionJobTemplate(ctx, jobID, *templateID); err != nil {
			// The log row still carries the id, so dedupe is intact; this is
			// only the queue-side projection losing the label.
			s.log.Warn("action: restore template id on retry failed", "job", jobID, "err", err)
			return rescheduled, nil
		}
		rescheduled.TemplateID = *templateID
	}
	return rescheduled, nil
}

// RunPendingNow pulls every PENDING job forward to now, so the workers claim
// them on their next poll instead of waiting out the backoff.
//
// This deliberately drops the per-account velocity gate's timing: that gate
// exists to space OUT retries against a platform, and an operator pressing this
// is saying "I know, run it now". The gate still applies to what happens after
// the claim, so a run that fails goes back to normal backoff rather than
// retrying immediately in a loop.
//
// Idempotent and safe to press twice: jobs already RUNNING or terminal are not
// touched, and a PENDING job already due is rewritten to the same instant.
//
// One statement, not a paged loop. The previous implementation read the PENDING
// set with LIMIT/OFFSET and rescheduled row by row; because ORDER BY
// scheduled_at was re-sorted by the very UPDATE it was walking, page 2 re-read
// rows page 1 had already moved: with two due batches, every distinct row after
// the first page was skipped while the moved ones were counted twice. The
// button means "set every PENDING row to now", so that is the statement.
func (s *ActionService) RunPendingNow(ctx context.Context) (int, error) {
	now := s.clock.Now()
	moved, err := s.actions.SetPendingActionJobsScheduledAt(ctx, now)
	if err != nil {
		return 0, fmt.Errorf("run pending now: %w", err)
	}
	return int(moved), nil
}

// Cancel makes one job terminal. PENDING and RUNNING are cancellable; anything
// already terminal is rejected rather than reported as a successful cancel, so
// the operator learns the verdict was not theirs to overwrite.
//
// Cancelling a RUNNING job does not stop the worker mid-action: Playwright may
// already have posted. What it does guarantee is that the late callback is a
// no-op (CompleteActionJob's status guard) and the stale-running sweep cannot
// requeue the job, so the cancel can never cause a second delivery.
func (s *ActionService) Cancel(ctx context.Context, jobID string) (domain.ActionJob, error) {
	job, err := s.actions.GetActionJob(ctx, jobID)
	if err != nil {
		return domain.ActionJob{}, fmt.Errorf("cancel action job: %w", err)
	}
	if job.Status != domain.JobStatusPending && job.Status != domain.JobStatusRunning {
		return domain.ActionJob{}, fmt.Errorf("%w: job %s is already %s", domain.ErrValidation, jobID, job.Status)
	}
	cancelled, err := s.actions.CancelActionJob(ctx, jobID)
	if err != nil {
		return domain.ActionJob{}, err
	}
	return cancelled, nil
}

// CancelPendingNow cancels the whole queued set in one statement and reports
// how many rows moved. 0 is a valid answer (nothing was queued), matching
// RunPendingNow's contract.
//
// RUNNING rows are deliberately not touched: the operator's bulk cancel is
// about the queue that has not started, and a row already dispatched is
// answered by the per-row cancel, which explains that it cannot stop the
// worker.
func (s *ActionService) CancelPendingNow(ctx context.Context) (int, error) {
	moved, err := s.actions.CancelPendingActionJobs(ctx)
	if err != nil {
		return 0, fmt.Errorf("cancel pending now: %w", err)
	}
	return int(moved), nil
}

// List returns the queue newest-first with each job's latest attempt verdict.
// One batched log query keeps a page at two queries, not N+1.
func (s *ActionService) List(ctx context.Context, status *domain.JobStatus, limit int) ([]ActionView, error) {
	if limit <= 0 || limit > MaxActionBatch {
		limit = MaxActionBatch
	}

	var jobs []domain.ActionJob
	var err error
	if status != nil && *status != "" {
		jobs, err = s.actions.ListActionJobsByStatus(ctx, *status, &limit, nil)
	} else {
		jobs, err = s.actions.ListActionJobs(ctx, &limit, nil)
	}
	if err != nil {
		return nil, fmt.Errorf("list action jobs: %w", err)
	}
	if len(jobs) == 0 {
		return nil, nil
	}

	ids := make([]string, 0, len(jobs))
	for _, j := range jobs {
		ids = append(ids, j.ID)
	}
	logs, err := s.actions.LatestActionLogsByJobs(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("latest action logs: %w", err)
	}
	byJob := make(map[string]domain.ActionLog, len(logs))
	for _, l := range logs {
		byJob[l.ActionJobID] = l
	}

	targetURLs := make(map[string]string)
	if tr, ok := s.scrapes.(targetReader); ok {
		for _, j := range jobs {
			if j.TargetID == "" {
				continue
			}
			if _, seen := targetURLs[j.TargetID]; seen {
				continue
			}
			if tgt, err := tr.GetTarget(ctx, j.TargetID); err == nil {
				targetURLs[j.TargetID] = tgt.URL
			} else {
				targetURLs[j.TargetID] = ""
			}
		}
	}

	views := make([]ActionView, 0, len(jobs))
	for _, j := range jobs {
		v := ActionView{
			Job:       j,
			TargetURL: targetURLs[j.TargetID],
		}
		if l, ok := byJob[j.ID]; ok {
			v.RenderedText = l.RenderedText
			v.ErrorClass = l.ErrorClass
		}
		views = append(views, v)
	}
	return views, nil
}

// validateActionType rejects anything the queue JobType does not treat as an
// action. The enum is the contract the worker dispatches on.
func validateActionType(t domain.JobType) error {
	if !t.IsAction() {
		return fmt.Errorf("%w: action type is not a known action type, got %q", domain.ErrValidation, t)
	}
	return nil
}

// platformHosts is the per-platform host allowlist. The worker opens a target
// URL in the account's live, session-hydrated browser, so an operator-supplied
// URL is a navigation primitive: without this, `act` can point a logged-in
// Instagram Chromium at the cloud metadata endpoint and the worker's evidence
// screenshot becomes a response body.
//
// ponytail: one map literal per platform. Move to config only when a platform
// is added that cannot be enumerated here.
var platformHosts = map[domain.Platform][]string{
	domain.PlatformInstagram: {"instagram.com", "www.instagram.com"},
	domain.PlatformThreads:   {"threads.net", "www.threads.net", "threads.com", "www.threads.com"},
	// Read-only scrape surface: no worker acts on these yet, but the on-demand
	// scrape navigates to them, so the same SSRF floor applies.
	domain.PlatformTikTok:  {"tiktok.com", "www.tiktok.com", "m.tiktok.com"},
	domain.PlatformYouTube: {"youtube.com", "www.youtube.com", "m.youtube.com", "youtu.be"},
	domain.PlatformX:       {"x.com", "www.x.com", "twitter.com", "www.twitter.com", "mobile.twitter.com"},
}

// safeTargetURL is the enqueue floor (SSRF): http/https on a host that belongs
// to the acting account's platform. The scheme check is deliberately strict
// rather than a blocklist: a blocklist has to be kept current to be safe, a
// strict allowlist cannot drift.
func safeTargetURL(raw string, platform domain.Platform) (*url.URL, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, fmt.Errorf("%w: target URL must not be blank", domain.ErrValidation)
	}
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, fmt.Errorf("%w: invalid target URL: %v", domain.ErrValidation, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("%w: target URL scheme must be http or https, got %q", domain.ErrValidation, u.Scheme)
	}
	hosts, ok := platformHosts[platform]
	if !ok {
		return nil, fmt.Errorf("%w: no target hosts configured for %q", domain.ErrValidation, platform)
	}
	host := strings.ToLower(u.Hostname())
	match := false
	for _, h := range hosts {
		if host == h {
			match = true
			break
		}
	}
	if !match {
		return nil, fmt.Errorf("%w: %s host %q is not a %s target", domain.ErrValidation, platform, host, platform)
	}
	return u, nil
}

// commentIDFromURL extracts the `/c/<id>/` segment of an Instagram comment
// permalink. Empty means the URL addresses a post, not a specific comment —
// which is exactly the input a reply action must never accept (see Enqueue).
func commentIDFromURL(raw string) string {
	u := raw
	if i := strings.IndexAny(u, "?#"); i >= 0 {
		u = u[:i]
	}
	segs := strings.Split(strings.Trim(u, "/"), "/")
	for i := 0; i+1 < len(segs); i++ {
		if segs[i] == "c" {
			return segs[i+1]
		}
	}
	return ""
}

// replyTargetID extracts the comment id a reply URL addresses, in the platform's
// own permalink shape:
//
//   - Instagram: `/p/<post>/c/<commentId>/` → the segment after `c`.
//   - Threads:   `/post/<commentCode>` → the comment's OWN post code. Threads
//     has no `/c/` route (it 404s); a reply is a post, so its code is the
//     address Enqueue links and the worker opens.
//
// Empty means the URL does not name a specific comment, which a reply job must
// never accept.
//
// ponytail: on Threads a comment and a top-level post are the same object, so
// the URL alone cannot tell them apart — a pasted POST permalink passes here
// and the worker replies at the post's top level. The comment table cannot be
// consulted without a store on this path; if that matters, reject Threads reply
// targets whose code is not a scraped comment (needs a comment lookup in
// Enqueue, which already does one for the link).
func replyTargetID(p domain.Platform, path string) string {
	if p == domain.PlatformThreads {
		return postCodeFromPath(path)
	}
	return commentIDFromURL(path)
}

// replyPermalinkShape is the human-facing shape hint in the Enqueue rejection,
// kept next to replyTargetID so the two cannot drift.
func replyPermalinkShape(p domain.Platform) string {
	if p == domain.PlatformThreads {
		return "https://www.threads.net/post/<commentCode>"
	}
	return "https://www.instagram.com/p/<postCode>/c/<commentId>/"
}

// postCodeFromPath returns the shortcode of a Threads `/post/<code>` permalink
// (bare or under `/@handle`), or "" for any other shape. A `/c/` suffix is
// rejected outright: that IG-style route does not exist on Threads (it 404s),
// so accepting it would enqueue a reply against the POST instead of the
// comment the operator meant.
func postCodeFromPath(raw string) string {
	u := raw
	if i := strings.IndexAny(u, "?#"); i >= 0 {
		u = u[:i]
	}
	segs := strings.Split(strings.Trim(u, "/"), "/")
	for i, seg := range segs {
		if seg != "post" {
			continue
		}
		if i+1 >= len(segs) || i+1 != len(segs)-1 {
			return ""
		}
		return segs[i+1]
	}
	return ""
}
