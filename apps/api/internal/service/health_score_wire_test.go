package service

import (
	"context"
	"testing"

	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/domain"
)

// healthActionStore embeds the retry-test fake and only overrides the lookup the
// health path needs, so this test does not have to re-implement ActionStore.
type healthActionStore struct {
	*fakeActionStore
	job domain.ActionJob
}

func (s *healthActionStore) GetActionJob(_ context.Context, id string) (domain.ActionJob, error) {
	if id != s.job.ID {
		return domain.ActionJob{}, domain.ErrNotFound
	}
	return s.job, nil
}

// TestApplyHealth_QuarantinesFailingAccount is the P5-02 end-to-end check: a
// stream of failed attempts drives the score down and the account out of the
// pool, without the caller having to know the threshold.
func TestApplyHealth_QuarantinesFailingAccount(t *testing.T) {
	accounts := newFakeAccountStore()
	acc := accountFixture("h1", domain.PlatformInstagram)
	acc.HealthScore = 100
	accounts.seed(acc)

	svc := NewJobService(
		newFakeWorkerStore(),
		accounts,
		nil,
		&healthActionStore{
			fakeActionStore: newFakeActionStore(),
			job:             domain.ActionJob{ID: "job-1", AccountID: "h1", Status: domain.JobStatusRunning},
		},
		nil,
		nil,
		nil,
	)

	fail := AttemptRecord{AttemptID: "job-1#1", Status: domain.AttemptFailed, ErrorClass: domain.ErrorClassTransient}

	// Seven transient failures at -10 each: 100 -> 30. The threshold is
	// exclusive, so the account is still in the pool at exactly 30; one more
	// failure drops it to 20 and out of the pool.
	for i := 0; i < 7; i++ {
		svc.applyHealth(context.Background(), "job-1", fail)
	}
	if got, _ := accounts.GetByID(context.Background(), "h1"); got.Status != domain.AccountActive {
		t.Errorf("after 7 failures status = %q, want ACTIVE (score 30 is not yet quarantined)", got.Status)
	}
	if got, _ := accounts.GetByID(context.Background(), "h1"); got.HealthScore != 30 {
		t.Errorf("score = %d, want 30 after 7 failures", got.HealthScore)
	}

	svc.applyHealth(context.Background(), "job-1", fail)
	got, _ := accounts.GetByID(context.Background(), "h1")
	if got.Status != domain.AccountQuarantined {
		t.Errorf("after 8 failures status = %q, want QUARANTINED", got.Status)
	}
	if got.HealthScore != 20 {
		t.Errorf("score = %d, want 20 (100 - 8*10)", got.HealthScore)
	}
	if got.IsPackable() {
		t.Error("a quarantined account must not be packable")
	}
}

// TestApplyHealth_BannedIsTerminal checks the one class that does not wait for
// the score to fall: a BANNED verdict kills the account immediately, so a
// healthy-looking account cannot keep drawing work after the platform rejects it.
func TestApplyHealth_BannedIsTerminal(t *testing.T) {
	accounts := newFakeAccountStore()
	acc := accountFixture("h2", domain.PlatformInstagram)
	acc.HealthScore = 100
	accounts.seed(acc)

	svc := NewJobService(
		newFakeWorkerStore(),
		accounts,
		nil,
		&healthActionStore{
			fakeActionStore: newFakeActionStore(),
			job:             domain.ActionJob{ID: "job-2", AccountID: "h2", Status: domain.JobStatusRunning},
		},
		nil,
		nil,
		nil,
	)

	svc.applyHealth(context.Background(), "job-2", AttemptRecord{
		AttemptID:  "job-2#1",
		Status:     domain.AttemptFailed,
		ErrorClass: domain.ErrorClassBanned,
	})

	got, _ := accounts.GetByID(context.Background(), "h2")
	if got.Status != domain.AccountDead {
		t.Errorf("status = %q, want DEAD after a BANNED verdict", got.Status)
	}
	if got.HealthScore != 0 {
		t.Errorf("score = %d, want 0 for a dead account", got.HealthScore)
	}
	if got.IsPackable() {
		t.Error("a dead account must not be packable")
	}
}

// TestApplyHealth_SuccessRecovers proves the pool heals on its own: a quarantined
// account that starts succeeding again climbs back above the threshold. The
// status itself is not cleared here (that is the operator's resume), but the
// score must move up or a single bad hour permanently costs the account.
func TestApplyHealth_SuccessRecovers(t *testing.T) {
	accounts := newFakeAccountStore()
	acc := accountFixture("h3", domain.PlatformInstagram)
	acc.HealthScore = 25
	accounts.seed(acc)

	svc := NewJobService(
		newFakeWorkerStore(),
		accounts,
		nil,
		&healthActionStore{
			fakeActionStore: newFakeActionStore(),
			job:             domain.ActionJob{ID: "job-3", AccountID: "h3", Status: domain.JobStatusRunning},
		},
		nil,
		nil,
		nil,
	)

	ok := AttemptRecord{AttemptID: "job-3#1", Status: domain.AttemptSuccess}
	svc.applyHealth(context.Background(), "job-3", ok)

	got, _ := accounts.GetByID(context.Background(), "h3")
	if got.HealthScore != 30 {
		t.Errorf("score = %d, want 30 (25 + 5 recovery)", got.HealthScore)
	}
}

// TestApplyHealth_NoopWithoutAccount covers the wiring contract: a callback for a
// job with no account (a malformed row or an unassigned job) must not panic and
// must not write anything.
func TestApplyHealth_NoopWithoutAccount(t *testing.T) {
	accounts := newFakeAccountStore()
	svc := NewJobService(
		newFakeWorkerStore(),
		accounts,
		nil,
		&healthActionStore{
			fakeActionStore: newFakeActionStore(),
			job:             domain.ActionJob{ID: "job-4"}, // no AccountID
		},
		nil,
		nil,
		nil,
	)

	svc.applyHealth(context.Background(), "job-4", AttemptRecord{
		AttemptID:  "job-4#1",
		Status:     domain.AttemptFailed,
		ErrorClass: domain.ErrorClassBanned,
	})

	if got, err := accounts.GetByID(context.Background(), "ghost"); err != domain.ErrNotFound {
		t.Errorf("expected no account write, got %+v err=%v", got, err)
	}
}
