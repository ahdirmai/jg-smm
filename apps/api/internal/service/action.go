package service

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm/apps/api/internal/port"
)

// MaxActionBatch is the enqueue cap (P3-13 "queue 50"): one request may carry
// up to this many intents, and the queue page shows the same window.
const MaxActionBatch = 50

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
}

// ActionTextTTL bounds how long a per-account comment body waits for dispatch.
// Generous relative to the pacing gate (minutes) so a queued custom comment is
// never dropped before its job runs, but not unbounded.
const ActionTextTTL = 24 * 60 * 60 // seconds (24h)

// narrowTargetWriter is the slice of port.ScrapeStore the enqueue path needs:
// turn a pasted permalink into a Target row (find-or-create) so the scheduler
// can resolve it back to a URL at dispatch.
type narrowTargetWriter interface {
	UpsertTarget(ctx context.Context, t domain.Target) (domain.Target, error)
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

// ActionView is one queue row: the job's live status plus the latest attempt's
// verdict, which is what an operator actually reads.
type ActionView struct {
	Job          domain.ActionJob
	TargetURL    string
	RenderedText string
	ErrorClass   string
}

// NewActionService wires the service. A nil clock defaults to the system clock.
func NewActionService(
	actions port.ActionStore,
	accounts narrowAccount,
	scrapes narrowTargetWriter,
	clock port.Clock,
	logger *slog.Logger,
	texts port.ActionTextStore,
) *ActionService {
	if logger == nil {
		logger = slog.Default()
	}
	if clock == nil {
		clock = systemClock{}
	}
	return &ActionService{actions: actions, accounts: accounts, scrapes: scrapes, clock: clock, log: logger, texts: texts}
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
		u, err := safeTargetURL(item.TargetURL)
		if err != nil {
			return nil, fmt.Errorf("item %d: %w", i, err)
		}
		if text := strings.TrimSpace(item.Text); text != "" {
			if len(text) > MaxCommentTextLen {
				return nil, fmt.Errorf("item %d: %w: comment text is %d chars, the cap is %d", i, domain.ErrValidation, len(text), MaxCommentTextLen)
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

		// The account decides the platform, and a target must live under the
		// same platform as the account acting on it.
		acc, err := s.accounts.GetByID(ctx, item.AccountID)
		if err != nil {
			return nil, fmt.Errorf("item %d: account lookup: %w", i, err)
		}
		if !acc.IsPackable() {
			return nil, fmt.Errorf("item %d: %w: account %s is %s", i, domain.ErrValidation, acc.ID, acc.Status)
		}
		platforms[item.AccountID] = acc.Platform

		tgt, err := s.scrapes.UpsertTarget(ctx, domain.Target{
			Kind:       domain.TargetKindPost,
			Platform:   acc.Platform,
			ExternalID: u.String(),
			URL:        u.String(),
		})
		if err != nil {
			return nil, fmt.Errorf("item %d: target upsert: %w", i, err)
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
	s.log.Info("actions enqueued", "count", len(created))
	return created, nil
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

	views := make([]ActionView, 0, len(jobs))
	for _, j := range jobs {
		v := ActionView{Job: j}
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

// safeTargetURL is the enqueue floor (SSRF): only http/https on a host. The
// worker later opens this URL in a browser, so a file:// or internal address
// must never survive the API. The scheme check is deliberately strict rather
// than a blocklist: a blocklist has to be kept current to be safe, a strict
// allowlist cannot drift.
func safeTargetURL(raw string) (*url.URL, error) {
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
	if u.Host == "" {
		return nil, fmt.Errorf("%w: target URL must have a host", domain.ErrValidation)
	}
	return u, nil
}
