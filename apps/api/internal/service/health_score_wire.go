package service

import (
	"context"
	"encoding/json"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm/apps/api/internal/port"
)

// applyHealth (P5-02) walks one attempt verdict into the owning account's
// health score, and quarantines the account when it degrades past the policy
// threshold. It runs after the verdict is already persisted and the retry
// decision is already made, so it is strictly best-effort: a failure here is
// logged and swallowed, because a health update must never turn a successful
// callback into a 500 the worker will re-send.
//
// The account is looked up from the job, not the attempt record: the worker
// reports its own ID and the attempt ID, and only the job row knows which
// account the action belonged to.
func (s *JobService) applyHealth(ctx context.Context, jobID string, r AttemptRecord) {
	if s.accounts == nil || jobID == "" {
		// Pre-P3 wiring or a malformed callback: nothing to score against.
		return
	}
	if r.Status == domain.AttemptRunning {
		return
	}

	job, err := s.actions.GetActionJob(ctx, jobID)
	if err != nil {
		s.logger.Warn("health: lookup job", "jobId", jobID, "err", err)
		return
	}
	if job.AccountID == "" {
		// Unassigned jobs (and the retry unit-test fakes, whose job rows carry
		// no account) have nothing to penalise.
		return
	}

	acc, err := s.accounts.GetByID(ctx, job.AccountID)
	if err != nil {
		s.logger.Warn("health: lookup account", "accountId", job.AccountID, "err", err)
		return
	}

	acc.HealthScore = DefaultHealthScorePolicy.ApplyVerdict(acc.HealthScore, r.Status, r.ErrorClass)

	// BANNED is terminal: the platform has said the account is gone, so it
	// leaves the pool as DEAD rather than QUARANTINED. Everything else that
	// crosses the threshold is QUARANTINED, which keeps the row and the session
	// PVC for an operator to re-login or retire deliberately.
	if DefaultHealthScorePolicy.IsTerminal(r.Status, r.ErrorClass) {
		acc.Status = domain.AccountDead
	} else if DefaultHealthScorePolicy.ShouldQuarantine(acc.HealthScore, r.Status, r.ErrorClass) {
		acc.Status = domain.AccountQuarantined
	}

	if _, err := s.accounts.Update(ctx, acc); err != nil {
		s.logger.Warn("health: update account", "accountId", acc.ID, "err", err)
		return
	}

	switch acc.Status {
	case domain.AccountDead:
		s.logger.Warn("account marked dead", "accountId", acc.ID, "score", acc.HealthScore, "errorClass", string(r.ErrorClass))
	case domain.AccountQuarantined:
		s.logger.Warn("account auto-quarantined", "accountId", acc.ID, "score", acc.HealthScore, "errorClass", string(r.ErrorClass))
	}
	s.observeOutcome(acc, r)
	s.publishAccountHealth(ctx, acc)
}

// observeOutcome records the verdict as Prometheus metrics (P5-03): one counter
// per attempt outcome and the account's new health as a gauge. Gauges are set,
// not added, so a reconcile loop re-publishing the same fleet is idempotent.
// A nil registry (tests, or a boot without metrics) is fine: the write already
// landed, the scrape just will not show it.
func (s *JobService) observeOutcome(acc domain.Account, r AttemptRecord) {
	if s.metrics == nil {
		return
	}
	s.metrics.Actions.WithLabelValues(
		acc.ID,
		string(acc.Platform),
		derefStrPtr(r.ActionType),
		string(r.Status),
		string(r.ErrorClass),
	).Inc()
	if r.DurationMs > 0 {
		s.metrics.ActionDuration.WithLabelValues(
			string(acc.Platform),
			derefStrPtr(r.ActionType),
			string(r.Status),
		).Observe(float64(r.DurationMs) / 1000)
	}
	s.metrics.WorkerHealth.WithLabelValues(acc.ID, string(acc.Platform)).Set(float64(acc.HealthScore))
}

// publishAccountHealth fans the new health state to dashboards. Reuses the
// account-updated channel: the row is the source of truth, so the browser just
// reconciles the same way it does for a pause or a resume.
func (s *JobService) publishAccountHealth(ctx context.Context, acc domain.Account) {
	if s.stream == nil {
		return
	}
	view := toAccountView(acc)
	body, err := json.Marshal(view)
	if err != nil {
		s.logger.Warn("health: marshal account", "err", err)
		return
	}
	s.stream.Publish(ctx, port.EventAccountUpdated, body)
}
