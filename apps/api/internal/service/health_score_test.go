package service

import (
	"testing"
	"time"

	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/domain"
)

func TestHealthScorePolicy_ApplyVerdict(t *testing.T) {
	p := DefaultHealthScorePolicy

	tests := []struct {
		name   string
		score  int
		status domain.AttemptStatus
		class  domain.ErrorClass
		want   int
	}{
		{
			name:   "success recovers but caps at 100",
			score:  98,
			status: domain.AttemptSuccess,
			class:  "",
			want:   100,
		},
		{
			name:   "cancelled also recovers",
			score:  40,
			status: domain.AttemptCancelled,
			class:  "",
			want:   45,
		},
		{
			name:   "transient failure costs the default penalty",
			score:  80,
			status: domain.AttemptFailed,
			class:  domain.ErrorClassTransient,
			want:   70,
		},
		{
			name:   "unknown failure costs the default penalty",
			score:  80,
			status: domain.AttemptFailed,
			class:  domain.ErrorClassUnknown,
			want:   70,
		},
		{
			name:   "auth costs more than a timeout",
			score:  80,
			status: domain.AttemptFailed,
			class:  domain.ErrorClassAuth,
			want:   60,
		},
		{
			name:   "rate limit is the cheapest failure",
			score:  80,
			status: domain.AttemptFailed,
			class:  domain.ErrorClassRateLimit,
			want:   75,
		},
		{
			name:   "banned drives any account to zero",
			score:  100,
			status: domain.AttemptFailed,
			class:  domain.ErrorClassBanned,
			want:   0,
		},
		{
			name:   "failure never goes below zero",
			score:  3,
			status: domain.AttemptFailed,
			class:  domain.ErrorClassTransient,
			want:   0,
		},
		{
			name:   "running leaves the score alone",
			score:  77,
			status: domain.AttemptRunning,
			class:  "",
			want:   77,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := p.ApplyVerdict(tt.score, tt.status, tt.class); got != tt.want {
				t.Errorf("ApplyVerdict(%d, %s, %q) = %d, want %d", tt.score, tt.status, tt.class, got, tt.want)
			}
		})
	}
}

// Recovery must be slower than decay: this is the invariant that stops a
// failing account from oscillating around the quarantine threshold forever.
// Two successes (+5 each) exactly cancel one transient failure (-10), so the
// property that actually holds — and the one that matters — is that an account
// alternating failure/success trends downward, not that any number of wins is
// free.
func TestHealthScorePolicy_RecoveryIsSlowerThanDecay(t *testing.T) {
	p := DefaultHealthScorePolicy
	if p.RecoverBonus >= p.FailPenalty {
		t.Fatalf("RecoverBonus (%d) must stay below FailPenalty (%d)", p.RecoverBonus, p.FailPenalty)
	}

	// One success does not undo one failure: a fail/success pair must lose ground.
	oneEach := p.ApplyVerdict(p.ApplyVerdict(80, domain.AttemptFailed, domain.ErrorClassTransient), domain.AttemptSuccess, "")
	if oneEach >= 80 {
		t.Errorf("a failure+success pair recovered to %d, want a net loss from 80", oneEach)
	}

	// An account that alternates fails and successes slides toward quarantine,
	// rather than living at a constant score forever.
	score := 80
	for i := 0; i < 20; i++ {
		score = p.ApplyVerdict(score, domain.AttemptFailed, domain.ErrorClassTransient)
		score = p.ApplyVerdict(score, domain.AttemptSuccess, "")
	}
	if score >= 80 {
		t.Errorf("alternating fail/success ended at %d, want a downward drift from 80", score)
	}
}

func TestHealthScorePolicy_ShouldQuarantine(t *testing.T) {
	p := DefaultHealthScorePolicy

	tests := []struct {
		name   string
		score  int
		status domain.AttemptStatus
		class  domain.ErrorClass
		want   bool
	}{
		{name: "healthy account stays in the pool", score: 90, status: domain.AttemptSuccess, class: "", want: false},
		{name: "threshold is exclusive", score: 30, status: domain.AttemptFailed, class: domain.ErrorClassTransient, want: false},
		{name: "one below threshold quarantines", score: 29, status: domain.AttemptFailed, class: domain.ErrorClassTransient, want: true},
		{name: "zero quarantines", score: 0, status: domain.AttemptFailed, class: domain.ErrorClassTransient, want: true},
		{name: "banned quarantines regardless of score", score: 100, status: domain.AttemptFailed, class: domain.ErrorClassBanned, want: true},
		{name: "banned verdict on a success does not quarantine", score: 100, status: domain.AttemptSuccess, class: domain.ErrorClassBanned, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := p.ShouldQuarantine(tt.score, tt.status, tt.class); got != tt.want {
				t.Errorf("ShouldQuarantine(%d, %s, %q) = %v, want %v", tt.score, tt.status, tt.class, got, tt.want)
			}
		})
	}
}

func TestHealthScorePolicy_IsTerminal(t *testing.T) {
	p := DefaultHealthScorePolicy

	if !p.IsTerminal(domain.AttemptFailed, domain.ErrorClassBanned) {
		t.Error("a BANNED failure must be terminal")
	}
	if p.IsTerminal(domain.AttemptFailed, domain.ErrorClassAuth) {
		t.Error("AUTH failures are recoverable by re-login, not terminal")
	}
	if p.IsTerminal(domain.AttemptSuccess, domain.ErrorClassBanned) {
		t.Error("a success is never terminal, even with a stale class")
	}
}

func TestRestartFlapPolicy_IsFlapping(t *testing.T) {
	p := DefaultRestartFlapPolicy
	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)

	restart := func(minsAgo int) time.Time { return now.Add(-time.Duration(minsAgo) * time.Minute) }

	tests := []struct {
		name     string
		restarts []time.Time
		want     bool
	}{
		{name: "no restarts", restarts: nil, want: false},
		{name: "two restarts is not yet flapping", restarts: []time.Time{restart(1), restart(2)}, want: false},
		{name: "three restarts inside the window flaps", restarts: []time.Time{restart(1), restart(5), restart(9)}, want: true},
		{name: "restarts outside the window do not count", restarts: []time.Time{restart(1), restart(25), restart(40)}, want: false},
		{name: "boundary restart counts as recent", restarts: []time.Time{restart(1), restart(5), restart(10)}, want: true},
		{name: "more than max still flaps", restarts: []time.Time{restart(1), restart(2), restart(3), restart(4)}, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := p.IsFlapping(tt.restarts, now); got != tt.want {
				t.Errorf("IsFlapping(%v) = %v, want %v", tt.restarts, got, tt.want)
			}
		})
	}
}
