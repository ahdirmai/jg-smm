package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm/apps/api/internal/obs"
	"github.com/ahdirmai/jg-smm/apps/api/internal/port"
)

// JobService records worker-reported outcomes. Workers never touch the DB
// (ADR 0011): they POST here and this service is the single writer for those
// rows, which lets it validate enums, truncate text and normalise paths.
type JobService struct {
	workers  port.WorkerStore
	accounts port.AccountStore
	logs     port.ProvisionLogStore
	// actions persists action verdicts (P3-11). Nil in earlier phases or tests;
	// a callback then reports persistence as unavailable instead of panicking.
	actions port.ActionStore
	// retry is the action-attempt retry policy (P3-11): how far an attempt may
	// escalate and how long to wait before the next one.
	retry RetryPolicy
	clock port.Clock
	// stream fans action verdicts to dashboards (P4-03). Optional: a nil
	// publisher means the callback still persists, the dashboard just polls.
	stream port.StreamPublisher
	// metrics is the Prometheus instrument set (P5-03). Optional for the same
	// reason: a nil registry means the callback still lands, the scrape just
	// does not see the verdict.
	metrics *obs.Metrics
	logger  *slog.Logger
}

// RetryPolicy is the action retry budget (P3-11): a retryable failure gets at
// most MaxAttempts tries total, spaced by escalating Backoff. 4xx-class
// verdicts never retry — the classifier marks those AUTH/BANNED/UNKNOWN, and
// re-running an action on a dead session cannot change the outcome.
type RetryPolicy struct {
	MaxAttempts int
	Backoff     time.Duration
}

// DefaultRetryPolicy is the ticket's contract: 3 attempts, escalating base
// backoff. The scheduler adds jitter; the value here is the floor.
var DefaultRetryPolicy = RetryPolicy{MaxAttempts: 3, Backoff: 30 * time.Second}

// NewJobService wires the service. workers/accounts/logs/actions may be nil
// during earlier phases; the affected endpoints then report the store as
// unavailable rather than panicking.
func NewJobService(workers port.WorkerStore, accounts port.AccountStore, logs port.ProvisionLogStore, actions port.ActionStore, clock port.Clock, stream port.StreamPublisher, metrics *obs.Metrics, logger *slog.Logger) *JobService {
	if logger == nil {
		logger = slog.Default()
	}
	if clock == nil {
		clock = realClock{}
	}
	return &JobService{
		workers:  workers,
		accounts: accounts,
		logs:     logs,
		actions:  actions,
		retry:    DefaultRetryPolicy,
		clock:    clock,
		stream:   stream,
		metrics:  metrics,
		logger:   logger,
	}
}

// AttemptRecord is a sanitised action-attempt verdict.
type AttemptRecord struct {
	AttemptID  string
	Status     domain.AttemptStatus
	Screenshot string
	Error      *string
	// ErrorClass is the coarse classification of a failure (P3-12): the retry
	// policy branches on it and the dashboard groups failures by it. It is
	// derived here, from the worker's free-text error, so the writer of the
	// action_log never has to trust a class the caller remembered to set.
	ErrorClass domain.ErrorClass
	ActionType *string
	TargetURL  *string
	WorkerID   *string
	// RenderedText is the final comment text the worker posted; the log stores
	// it so a verdict is replayable without the template pool.
	RenderedText *string
	// ResponseExcerpt is the first runes of the platform response (debug trail).
	ResponseExcerpt *string
	// DurationMs is the measured run time of the attempt.
	DurationMs int64
}

// ClassifyAttempt returns the error class for one attempt verdict (P3-12). A
// success or a cancellation carries no class; only a failure or a retry needs
// one, and the class is derived from the worker's free-text error so the
// action_log writer and the retry policy agree. Exported because the callback
// handler and the retry scheduler both branch on it.
func ClassifyAttempt(status domain.AttemptStatus, errMsg string) domain.ErrorClass {
	if status != domain.AttemptFailed && status != domain.AttemptRetry {
		return ""
	}
	return domain.ClassifyActionError(errMsg)
}

// RecordAttempt persists an action verdict (P3-11). It is the single writer
// for action_log rows: the worker only POSTs, so this service validates the
// enums, derives the error class, normalises the screenshot name, and applies
// the retry policy. Never throws: a malformed callback is a 400, a store
// failure is a 500 the worker's own 3x callback retry can ride over.
//
// Idempotency: the (action_job_id, attempt) pair is UNIQUE, so the RUNNING
// report and the terminal verdict are one row no matter how many times the
// callback is replayed — the Idempotency-Key of this endpoint IS that pair.
func (s *JobService) RecordAttempt(ctx context.Context, r AttemptRecord) error {
	if !r.Status.Valid() {
		return fmt.Errorf("%w: invalid attempt status %q", domain.ErrValidation, r.Status)
	}
	// Classify a failure at the boundary (P3-12): the worker reports free text,
	// the retry policy and the dashboard need a closed class. A success carries
	// no class, and an empty error string classifies as UNKNOWN (not retryable),
	// so a callback that reports failure with no reason cannot quietly retry.
	r.ErrorClass = ClassifyAttempt(r.Status, errorOrEmpty(r.Error))
	s.logger.Info("action callback received",
		"attemptId", r.AttemptID,
		"status", r.Status,
		"actionType", deref(r.ActionType),
		"workerId", deref(r.WorkerID),
		"screenshot", r.Screenshot,
		"errorClass", string(r.ErrorClass),
	)
	if s.actions == nil {
		return fmt.Errorf("%w: action store unavailable", domain.ErrUnavailable)
	}
	jobID, attempt, err := parseAttemptID(r.AttemptID)
	if err != nil {
		return fmt.Errorf("%w: %v", domain.ErrValidation, err)
	}
	// The verdict is written first: even the retry decision below reads from the
	// log, and a crash between the upsert and the reschedule leaves a FAILED
	// row, not a lost one.
	log := domain.ActionLog{
		ActionJobID:     jobID,
		Attempt:         attempt,
		Status:          r.Status,
		Verified:        r.Status == domain.AttemptSuccess,
		WorkerID:        derefStrPtr(r.WorkerID),
		RenderedText:    derefStrPtr(r.RenderedText),
		ResponseExcerpt: derefStrPtr(r.ResponseExcerpt),
		ErrorClass:      string(r.ErrorClass),
		ScreenshotURL:   r.Screenshot,
		DurationMs:      r.DurationMs,
	}
	if _, err := s.actions.UpsertActionLog(ctx, log); err != nil {
		return fmt.Errorf("persist attempt: %w", err)
	}
	// Fan the verdict to dashboards (P4-03): the queue page reads this frame
	// instead of polling. The frame carries the ids + verdict, not the job
	// row; the browser refetches the queue (ADR 0010: frame is a signal, the
	// read is authoritative) so a race between the log and the job row still
	// converges to the stored truth.
	s.publishAction(ctx, log)
	if err := s.applyVerdict(ctx, jobID, attempt, r); err != nil {
		return err
	}
	// P5-02: the verdict is durable, now let it cost or restore the account's
	// health. Best-effort and last, so a health write failure never turns an
	// already-recorded callback into a 500 the worker re-sends.
	s.applyHealth(ctx, jobID, r)
	return nil
}

// publishAction fans an action verdict to dashboards. A nil publisher is the
// "no realtime" mode: the write already landed, the queue just polls.
func (s *JobService) publishAction(ctx context.Context, l domain.ActionLog) {
	if s.stream == nil {
		return
	}
	body, err := json.Marshal(map[string]any{
		"jobId":        l.ActionJobID,
		"attempt":      l.Attempt,
		"status":       l.Status,
		"verified":     l.Verified,
		"errorClass":   l.ErrorClass,
		"renderedText": l.RenderedText,
	})
	if err != nil {
		s.logger.Warn("stream: marshal action frame", "err", err)
		return
	}
	s.stream.Publish(ctx, port.EventActionUpdated, body)
}

// AuthOutcome is a sanitised login/2FA result for an account.
type AuthOutcome struct {
	AccountID  string
	AuthStatus domain.AuthStatus
	Handle     *string
	Error      *string
}

// RecordAuthOutcome updates an account's login state machine.
func (s *JobService) RecordAuthOutcome(ctx context.Context, o AuthOutcome) error {
	if !o.AuthStatus.Valid() {
		return fmt.Errorf("%w: invalid auth status %q", domain.ErrValidation, o.AuthStatus)
	}
	if s.accounts == nil {
		return fmt.Errorf("%w: account store unavailable", domain.ErrUnavailable)
	}

	acc, err := s.accounts.GetByID(ctx, o.AccountID)
	if err != nil {
		return fmt.Errorf("lookup account %s: %w", o.AccountID, err)
	}

	acc.AuthStatus = o.AuthStatus
	switch o.AuthStatus {
	case domain.AuthAuthenticated:
		now := s.clock.Now()
		acc.LastVerified = &now
		if o.Handle != nil {
			acc.Handle = o.Handle
		}
		if acc.Status == domain.AccountPending {
			acc.Status = domain.AccountActive
		}
	case domain.AuthFailed, domain.AuthNeedsInput:
		if o.Error != nil {
			acc.LastError = o.Error
		}
	}

	if _, err := s.accounts.Update(ctx, acc); err != nil {
		return fmt.Errorf("update account %s: %w", o.AccountID, err)
	}
	s.logger.Info("account callback recorded",
		"accountId", o.AccountID,
		"authStatus", o.AuthStatus,
		"handle", deref(o.Handle),
	)
	return nil
}

// HeartbeatRecord is a sanitised worker telemetry sample.
type HeartbeatRecord struct {
	WorkerID      string
	BrowserStatus string
	QueueDepth    int
	CurrentJobID  *string
	CPU           float64
	Mem           float64
	JobsDone      int
	LastActionAt  *time.Time
	// NovncURL is the browser-reachable live-view URL (P4-08). A pointer so an
	// absent field is distinguishable from an explicitly cleared one.
	NovncURL *string
}

// RecordHeartbeat appends telemetry and refreshes the denormalised worker row.
// A heartbeat for an unknown worker is rejected (not silently created).
func (s *JobService) RecordHeartbeat(ctx context.Context, r HeartbeatRecord) error {
	if s.workers == nil {
		return fmt.Errorf("%w: worker store unavailable", domain.ErrUnavailable)
	}
	w, err := s.resolveWorker(ctx, r.WorkerID)
	if err != nil {
		return fmt.Errorf("lookup worker %s: %w", r.WorkerID, err)
	}

	hb := domain.Heartbeat{
		WorkerID: r.WorkerID,
		TS:       s.clock.Now(),
		CPU:      r.CPU,
		Mem:      r.Mem,
		JobsDone: r.JobsDone,
	}
	snap := port.WorkerSnapshot{
		Status:        s.heartbeatStatus(w, r),
		BrowserStatus: defaultStr(r.BrowserStatus, w.BrowserStatus),
		QueueDepth:    r.QueueDepth,
		CurrentJobID:  r.CurrentJobID,
		NovncURL:      r.NovncURL,
	}
	if err := s.workers.RecordHeartbeat(ctx, hb, snap); err != nil {
		return fmt.Errorf("record heartbeat: %w", err)
	}
	// Fan the new state to dashboards so a worker flipping PENDING → READY lands
	// live. The frame carries the full container (ADR 0010); the status on the
	// card is what the create-progress UI is waiting on.
	if s.stream != nil {
		accounts, err := s.accounts.ListByWorker(ctx, w.ID)
		if err != nil {
			s.logger.Warn("record heartbeat: list accounts failed", "workerId", w.ID, "err", err)
		}
		updated := w
		updated.Status = snap.Status
		updated.BrowserStatus = snap.BrowserStatus
		if snap.NovncURL != nil {
			updated.NoVNCService = snap.NovncURL
		}
		payload, err := json.Marshal(toContainerView(updated, accounts))
		if err != nil {
			s.logger.Warn("record heartbeat: marshal stream frame failed", "workerId", w.ID, "err", err)
			return nil
		}
		s.stream.Publish(ctx, port.EventWorkerHealth, payload)
	}
	return nil
}

// WorkerGeolocation is the frozen coordinate the worker spoofs with Playwright.
// Empty coordinates mean the worker row has no location (a row created before
// geolocation, or a worker id the API does not know); the caller treats that as
// "no spoofing" rather than an error.
type WorkerGeolocation struct {
	Latitude  float64
	Longitude float64
	Location  string
}

// Geolocation reads the worker's anchored point. A missing worker or a row
// without a location returns ErrNotFound: the worker falls back to the real GPS
// of nothing at all (it is a datacentre), which is fine but logged.
// resolveWorker finds the row a worker container is talking about. The worker
// boots with a hostname-derived id ("worker-<hostname>") and only learns the
// row's UUID after claiming, so both the pre-claim heartbeat and the geolocation
// probe arrive carrying the boot id. Try the UUID first (the post-claim path,
// and the k8s tier where the id is the pod name), then fall back to container_id
// (a locally scaled container mid-claim, or one that never finished claiming).
func (s *JobService) resolveWorker(ctx context.Context, workerID string) (domain.Worker, error) {
	w, err := s.workers.GetByID(ctx, workerID)
	if err == nil {
		return w, nil
	}
	if !errors.Is(err, domain.ErrNotFound) {
		return domain.Worker{}, err
	}
	// Not a row id: treat it as the container's boot identity.
	return s.workers.GetByContainerID(ctx, workerID)
}

// Claim is the handshake that makes a locally scaled worker usable. A container
// booted by `docker compose up --scale worker=N` knows nothing about the rows
// the dashboard created; it presents its boot identity and the redis keys it is
// already subscribed to, and this binds the oldest free PENDING row to it and
// hands back the row's real id + frozen GPS point.
//
// Without it the row stays PENDING forever: the container's heartbeats carry a
// hostname that matches no UUID, so no status ever flips, and jobs queued to the
// row land on a redis key nothing is listening on.
func (s *JobService) Claim(ctx context.Context, c WorkerClaim) (WorkerClaimResult, error) {
	if s.workers == nil {
		return WorkerClaimResult{}, fmt.Errorf("%w: worker store unavailable", domain.ErrUnavailable)
	}
	if c.ContainerID == "" {
		return WorkerClaimResult{}, fmt.Errorf("%w: container id is required", domain.ErrValidation)
	}

	channel := c.ControlChannel
	if channel == "" {
		channel = domain.ControlChannel(c.ContainerID)
	}
	queue := c.ActionQueue
	if queue == "" {
		queue = domain.ActionQueue(c.ContainerID)
	}
	pvc := c.SessionPVC
	if pvc == "" {
		pvc = domain.SessionPVCName(c.ContainerID)
	}

	w, err := s.workers.Claim(ctx, port.WorkerClaim{
		ContainerID:    c.ContainerID,
		ControlChannel: channel,
		ActionQueue:    queue,
		SessionPVC:     pvc,
		NovncURL:       c.NovncURL,
	})
	if err != nil {
		return WorkerClaimResult{}, err
	}

	// The dashboard fleet card shows the container binding, so fan it out the
	// same way a heartbeat does.
	if s.stream != nil {
		accounts, err := s.accounts.ListByWorker(ctx, w.ID)
		if err != nil {
			s.logger.Warn("claim: list accounts failed", "workerId", w.ID, "err", err)
		}
		payload, err := json.Marshal(toContainerView(w, accounts))
		if err == nil {
			s.stream.Publish(ctx, port.EventWorkerHealth, payload)
		}
	}

	out := WorkerClaimResult{WorkerID: w.ID, Name: w.Name, Region: w.Region}
	if w.Location != nil {
		out.Location = *w.Location
	}
	if w.Latitude != nil && w.Longitude != nil {
		out.Latitude, out.Longitude = *w.Latitude, *w.Longitude
	}
	return out, nil
}

// WorkerClaim is the worker's boot identity presented at claim time.
type WorkerClaim struct {
	ContainerID    string
	ControlChannel string
	ActionQueue    string
	SessionPVC     string
	NovncURL       *string
}

// WorkerClaimResult is what the worker needs back: the row's real id (so its
// heartbeats, queue key and control subscription all address the row) and the
// frozen GPS point it must spoof before its first job.
type WorkerClaimResult struct {
	WorkerID  string  `json:"workerId"`
	Name      string  `json:"name"`
	Region    string  `json:"region"`
	Location  string  `json:"location"`
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
}

func (s *JobService) Geolocation(ctx context.Context, workerID string) (WorkerGeolocation, error) {
	if s.workers == nil {
		return WorkerGeolocation{}, fmt.Errorf("%w: worker store unavailable", domain.ErrUnavailable)
	}
	w, err := s.resolveWorker(ctx, workerID)
	if err != nil {
		return WorkerGeolocation{}, fmt.Errorf("lookup worker %s: %w", workerID, err)
	}
	if w.Location == nil || w.Latitude == nil || w.Longitude == nil {
		return WorkerGeolocation{}, fmt.Errorf("%w: worker %s has no location set", domain.ErrNotFound, workerID)
	}
	return WorkerGeolocation{
		Latitude:  *w.Latitude,
		Longitude: *w.Longitude,
		Location:  *w.Location,
	}, nil
}

// heartbeatStatus derives the reported status: a worker reporting an error browser
// state is marked ERROR, otherwise its existing status is preserved.
func (s *JobService) heartbeatStatus(w domain.Worker, r HeartbeatRecord) domain.WorkerStatus {
	// A heartbeat is proof of life: the container booted, the browser came up,
	// and the worker found the API. Without this, a healthy heartbeat leaves a
	// freshly created worker pinned at PENDING forever, and the dashboard's
	// "starting…" spinner never resolves.
	if r.BrowserStatus == "error" {
		return domain.WorkerError
	}
	// Preserve an operator's hold (DRAINING/QUARANTINED) — a live heartbeat does
	// not override a deliberate pause; only the PENDING/ERROR stale states clear.
	switch w.Status {
	case domain.WorkerPending, domain.WorkerError, domain.WorkerDead:
		if r.CurrentJobID != nil {
			return domain.WorkerBusy
		}
		return domain.WorkerReady
	}
	// IDLE/BUSY/DRAINING/QUARANTINED/READY already reflect runtime state; the
	// current-job flag is the only thing a heartbeat can still change.
	if r.CurrentJobID != nil {
		return domain.WorkerBusy
	}
	if w.Status == domain.WorkerBusy {
		return domain.WorkerIdle
	}
	return w.Status
}

// realClock is the default port.Clock implementation.
type realClock struct{}

func (realClock) Now() time.Time { return time.Now().UTC() }

// parseAttemptID splits the callback's "<jobId>:<attempt>" key. The pair is the
// idempotency key of the whole endpoint: it names exactly one row in
// action_log, so a replayed callback updates the same verdict instead of
// appending a duplicate.
func parseAttemptID(id string) (string, int, error) {
	parts := strings.SplitN(id, ":", 2)
	if len(parts) != 2 || parts[0] == "" {
		return "", 0, fmt.Errorf("attempt_id must be \"<jobId>:<attempt>\", got %q", id)
	}
	n, err := strconv.Atoi(parts[1])
	if err != nil || n <= 0 {
		return "", 0, fmt.Errorf("attempt number must be a positive integer, got %q", parts[1])
	}
	return parts[0], n, nil
}

// applyVerdict projects a terminal verdict onto the job row and decides the
// retry. The ActionLog row is already written and is the source of truth; this
// only keeps the queue listable without a join and returns a retryable job to
// the pool when the policy allows it.
//
// RUNNING is explicitly NOT a verdict: it is the worker announcing it started,
// so it writes a log row and touches nothing else. Treating it as terminal
// would complete a job the worker has not finished yet.
//
// Retry rule (P3-11): only a class marked Retryable gets another attempt, and
// only while the attempt budget holds. AUTH and BANNED never retry — the
// session is gone or the account is locked, and a repeat cannot fix either. A
// non-retryable failure or a success completes the job immediately.
func (s *JobService) applyVerdict(ctx context.Context, jobID string, attempt int, r AttemptRecord) error {
	if r.Status == domain.AttemptRunning {
		return nil
	}
	if r.Status != domain.AttemptFailed && r.Status != domain.AttemptRetry {
		// SUCCESS or CANCELLED: terminal. Verified is set on the log row; the
		// job row just needs the projection.
		status := domain.JobStatusSuccess
		if r.Status == domain.AttemptCancelled {
			status = domain.JobStatusCancelled
		}
		if _, err := s.actions.CompleteActionJob(ctx, jobID, status, nil); err != nil {
			return fmt.Errorf("complete job: %w", err)
		}
		return nil
	}

	// A failure decides here whether the job gets another life.
	if r.ErrorClass.Retryable() && attempt < s.retry.MaxAttempts {
		backoff := s.retry.Backoff << (attempt - 1)
		at := s.clock.Now().Add(backoff)
		if _, err := s.actions.RescheduleActionJob(ctx, jobID, at); err != nil {
			return fmt.Errorf("reschedule retry: %w", err)
		}
		s.logger.Info("action retry scheduled",
			"jobId", jobID,
			"attempt", attempt,
			"errorClass", string(r.ErrorClass),
			"backoffMs", backoff.Milliseconds(),
		)
		return nil
	}

	// Budget exhausted or the class is final (AUTH/BANNED/UNKNOWN): the job is
	// FAILED with the reason preserved on the row for triage.
	if _, err := s.actions.CompleteActionJob(ctx, jobID, domain.JobStatusFailed, r.Error); err != nil {
		return fmt.Errorf("fail job: %w", err)
	}
	return nil
}

// errorOrEmpty unboxes a pointer-typed error message; nil is the empty string
// (the classifier maps that to UNKNOWN, which is not retryable).
func errorOrEmpty(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// derefStrPtr unboxes a pointer string to the plain string the domain uses for
// nullable text columns (empty means NULL at the repo boundary).
func derefStrPtr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func deref[T any](p *T) any {
	if p == nil {
		return nil
	}
	return *p
}

func defaultStr(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}
