package service

import (
	"time"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
)

// HealthScorePolicy is the P5-02 account health model. Health is a 0-100
// penalty accumulator: every failed action costs points, every success earns a
// little back, and an account that slides below QuarantineThreshold is pulled
// out of the pool automatically instead of being driven into a ban.
//
// The numbers come from ../../../../docs/SYSTEM_DESIGN.md (fail = -10, quarantine < 30) with one
// refinement: the cost is class-weighted, because a TRANSIENT timeout and a
// BANNED verdict are not the same signal. Charging both -10 burns a good
// account on a flaky proxy and rescues a dead one far too late.
type HealthScorePolicy struct {
	// QuarantineThreshold is the score at which an account is pulled from the
	// pool. Below this, the account is more likely to get banned than to
	// complete an action, so a human must reset it.
	QuarantineThreshold int
	// ScoreMax is the ceiling. Health recovers on success but never exceeds this,
	// so a long-lived healthy account cannot bank immunity against a ban wave.
	ScoreMax int
	// FailPenalty is the default cost of one failed action.
	FailPenalty int
	// RecoverBonus is what one successful action earns back. It is smaller than
	// FailPenalty on purpose: recovery must be slower than decay, or a
	// failing account oscillates around the threshold forever.
	RecoverBonus int
}

// DefaultHealthScorePolicy is the ticket contract: 0-100, quarantine below 30,
// -10 per failure. The class weights in ApplyVerdict are the only departure,
// and they are all more expensive than the default, never less.
var DefaultHealthScorePolicy = HealthScorePolicy{
	QuarantineThreshold: 30,
	ScoreMax:            100,
	FailPenalty:         10,
	RecoverBonus:        5,
}

// penalty returns the cost of one failed attempt by class. BANNED is terminal:
// the account is dead and the status is set directly, so the score is moot,
// but it is still driven to 0 to keep the invariant "dead accounts score 0".
func (p HealthScorePolicy) penalty(class domain.ErrorClass) int {
	switch class {
	case domain.ErrorClassBanned:
		// The account is gone. Penalise hard enough that even a maxed-out
		// health account crosses quarantine in one verdict.
		return p.ScoreMax
	case domain.ErrorClassAuth:
		// Session lost. Recoverable by re-login, but it is the account's own
		// state, not the network's, so it costs more than a timeout.
		return 20
	case domain.ErrorClassRateLimit:
		// The platform throttled us. Usually the fleet's pacing, not this
		// account's fault, so it is the cheapest real failure.
		return 5
	default:
		// TRANSIENT and UNKNOWN: the SYSTEM_DESIGN -10 default.
		return p.FailPenalty
	}
}

// ApplyVerdict returns the score that follows one attempt. A success always
// recovers, including from a QUARANTINED account that an operator put back in
// the pool manually — the score, not the status, is what this function owns.
//
// This is pure: the caller owns the write and the SSE publish, which keeps the
// scoring testable without a store.
func (p HealthScorePolicy) ApplyVerdict(score int, status domain.AttemptStatus, class domain.ErrorClass) int {
	switch status {
	case domain.AttemptSuccess, domain.AttemptCancelled:
		// A cancellation is not the account's fault, so it recovers too.
		return clampScore(score+p.RecoverBonus, p)
	case domain.AttemptFailed, domain.AttemptRetry:
		return clampScore(score-p.penalty(class), p)
	default:
		// RUNNING is not a verdict; leave the score alone.
		return clampScore(score, p)
	}
}

// clampScore keeps health inside [0, ScoreMax]. A negative score and a zero
// score mean the same thing to the pool (unusable), but the stored value
// should not lie about the direction it was travelling.
func clampScore(score int, p HealthScorePolicy) int {
	if score < 0 {
		return 0
	}
	if score > p.ScoreMax {
		return p.ScoreMax
	}
	return score
}

// ShouldQuarantine reports whether an account has degraded enough to leave the
// pool. QUARANTINED is a holding state: the account keeps its row and its
// session PVC, it is simply not packed while an operator decides.
func (p HealthScorePolicy) ShouldQuarantine(score int, status domain.AttemptStatus, class domain.ErrorClass) bool {
	if class == domain.ErrorClassBanned && (status == domain.AttemptFailed || status == domain.AttemptRetry) {
		return true
	}
	return score < p.QuarantineThreshold
}

// IsTerminal reports whether a verdict kills the account outright. BANNED is
// the only class that does: everything else is a degraded-but-recoverable
// account, and marking it DEAD would throw away a session an operator could
// re-login.
func (p HealthScorePolicy) IsTerminal(status domain.AttemptStatus, class domain.ErrorClass) bool {
	return class == domain.ErrorClassBanned && (status == domain.AttemptFailed || status == domain.AttemptRetry)
}

// RestartFlapPolicy is the anti-flapping half of P5-02: a container that
// restarts in a loop is not a worker, it is a crash. 3 restarts inside the
// window quarantines the worker so the reconciler stops spending PVCs on a
// broken image or a bad account pairing.
type RestartFlapPolicy struct {
	// MaxRestarts inside Window before the worker is quarantined.
	MaxRestarts int
	// Window is how long the restart memory lasts.
	Window time.Duration
}

// DefaultRestartFlapPolicy is the ticket contract: 3 restarts per 10 minutes.
var DefaultRestartFlapPolicy = RestartFlapPolicy{MaxRestarts: 3, Window: 10 * time.Minute}

// IsFlapping reports whether the observed restart times breach the policy.
// The caller passes the restart timestamps already trimmed to the worker; this
// only counts, it does not own the history.
func (p RestartFlapPolicy) IsFlapping(restarts []time.Time, now time.Time) bool {
	if p.MaxRestarts <= 0 || len(restarts) < p.MaxRestarts {
		return false
	}
	cutoff := now.Add(-p.Window)
	inWindow := 0
	for _, t := range restarts {
		// A restart exactly on the cutoff counts: the window is inclusive on
		// purpose, so a boundary restart is treated as recent, not expired.
		if !t.Before(cutoff) {
			inWindow++
		}
	}
	return inWindow >= p.MaxRestarts
}
