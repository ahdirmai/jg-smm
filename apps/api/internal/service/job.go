package service

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/port"
)

// JobService records worker-reported outcomes. Workers never touch the DB
// (ADR 0011): they POST here and this service is the single writer for those
// rows, which lets it validate enums, truncate text and normalise paths.
type JobService struct {
	workers  port.WorkerStore
	accounts port.AccountStore
	logs     port.ProvisionLogStore
	clock    port.Clock
	logger   *slog.Logger
}

// NewJobService wires the service. workers/accounts/logs may be nil during
// earlier phases; the affected endpoints then report the store as unavailable
// rather than panicking.
func NewJobService(workers port.WorkerStore, accounts port.AccountStore, logs port.ProvisionLogStore, clock port.Clock, logger *slog.Logger) *JobService {
	if logger == nil {
		logger = slog.Default()
	}
	if clock == nil {
		clock = realClock{}
	}
	return &JobService{workers: workers, accounts: accounts, logs: logs, clock: clock, logger: logger}
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

// RecordAttempt persists an action verdict. The job tables land in P3; until
// then the verdict is validated and logged so the transport path is exercised
// end to end, and callers get a clear "not yet persisted" error they can act on.
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
	// P3 will persist this into action_log once that table exists.
	return fmt.Errorf("%w: action verdict persistence lands in P3", domain.ErrUnavailable)
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
}

// RecordHeartbeat appends telemetry and refreshes the denormalised worker row.
// A heartbeat for an unknown worker is rejected (not silently created).
func (s *JobService) RecordHeartbeat(ctx context.Context, r HeartbeatRecord) error {
	if s.workers == nil {
		return fmt.Errorf("%w: worker store unavailable", domain.ErrUnavailable)
	}
	w, err := s.workers.GetByID(ctx, r.WorkerID)
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
	}
	if err := s.workers.RecordHeartbeat(ctx, hb, snap); err != nil {
		return fmt.Errorf("record heartbeat: %w", err)
	}
	return nil
}

// heartbeatStatus derives the reported status: a worker reporting an error browser
// state is marked ERROR, otherwise its existing status is preserved.
func (s *JobService) heartbeatStatus(w domain.Worker, r HeartbeatRecord) domain.WorkerStatus {
	if r.BrowserStatus == "error" {
		return domain.WorkerError
	}
	return w.Status
}

// realClock is the default port.Clock implementation.
type realClock struct{}

func (realClock) Now() time.Time { return time.Now().UTC() }

// errorOrEmpty unboxes a pointer-typed error message; nil is the empty string
// (the classifier maps that to UNKNOWN, which is not retryable).
func errorOrEmpty(p *string) string {
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
