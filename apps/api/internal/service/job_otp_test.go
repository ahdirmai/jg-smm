package service

import (
	"context"
	"testing"
	"time"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm/apps/api/internal/port"
)

func TestOTPFailurePausesWorkerAndDisablesAutomaticRefresh(t *testing.T) {
	workers := newFakeWorkerStore()
	w := readyFixture(workerFixture("w1", domain.DesiredRunning, 1, intPtr(1)))
	workers.seed(w)
	accounts := newFakeAccountStore()
	a := accountFixtureWithWorker("a-otp", domain.PlatformInstagram, w.ID)
	expires := time.Now().Add(-time.Hour)
	a.CookieExpiryAt = &expires
	a.RefreshFailures = 1
	accounts.seed(a)
	svc := NewJobService(workers, accounts, nil, nil, nil, &captureStream{}, nil, nil)
	if err := svc.RecordAuthOutcome(context.Background(), AuthOutcome{
		AccountID: a.ID, AuthStatus: domain.AuthFailed, Error: strPtr("otp_required: perlu OTP"),
	}); err != nil {
		t.Fatal(err)
	}
	got := workers.get(w.ID)
	if got.DesiredState != domain.DesiredPaused || got.Status != domain.WorkerIdle {
		t.Fatalf("OTP worker must be off, got %s/%s", got.DesiredState, got.Status)
	}
	acc := accounts.get(a.ID)
	if acc.AuthStatus != domain.AuthFailed || acc.LastError == nil || *acc.LastError != "otp_required: perlu OTP" {
		t.Fatalf("OTP failure not recorded: %+v", acc)
	}
	if acc.CookieExpiryAt != nil || acc.RefreshFailures != 1 {
		t.Fatal("skipped OTP must stop automatic refresh without increasing failure streak")
	}
}

func TestOTPFailureImmediatelyStopsWorker(t *testing.T) {
	workers := newFakeWorkerStore()
	w := readyFixture(workerFixture("w1", domain.DesiredRunning, 1, intPtr(1)))
	workers.seed(w)
	accounts := newFakeAccountStore()
	accounts.seed(accountFixtureWithWorker("a-otp", domain.PlatformInstagram, w.ID))
	driver := newFakeDriver()
	driver.running[w.ID] = 1
	svc := NewJobService(workers, accounts, nil, nil, nil, nil, nil, nil).WithWorkerDriver(driver)
	if err := svc.RecordAuthOutcome(context.Background(), AuthOutcome{
		AccountID: "a-otp", AuthStatus: domain.AuthFailed, Error: strPtr("otp_required: perlu OTP"),
	}); err != nil {
		t.Fatal(err)
	}
	if _, state, _ := driver.Observe(context.Background(), w.ID); state != port.ContainerStopped {
		t.Fatal("OTP callback returned while the container was still running")
	}
}

func TestCaptchaDoesNotStopWorker(t *testing.T) {
	workers := newFakeWorkerStore()
	w := readyFixture(workerFixture("w1", domain.DesiredRunning, 1, intPtr(1)))
	workers.seed(w)
	accounts := newFakeAccountStore()
	accounts.seed(accountFixtureWithWorker("a-captcha", domain.PlatformInstagram, w.ID))
	driver := newFakeDriver()
	driver.running[w.ID] = 1
	svc := NewJobService(workers, accounts, nil, nil, nil, nil, nil, nil).WithWorkerDriver(driver)
	if err := svc.RecordAuthOutcome(context.Background(), AuthOutcome{
		AccountID: "a-captcha", AuthStatus: domain.AuthNeedsInput, Error: strPtr("captcha: complete challenge"),
	}); err != nil {
		t.Fatal(err)
	}
	if _, state, _ := driver.Observe(context.Background(), w.ID); state != port.ContainerRunning {
		t.Fatal("CAPTCHA must remain visible in the running browser")
	}
}

func TestElasticPoolDoesNotResumeOTPWorker(t *testing.T) {
	workers := newFakeWorkerStore()
	w := readyFixture(workerFixture("w1", domain.DesiredPaused, 1, intPtr(1)))
	w.Source = domain.SourceAuto
	w.LastError = strPtr("otp_required: perlu OTP")
	workers.seed(w)
	p := NewElasticPool(workers, newFakeDriver(), fixedClock{t: time.Now()}, 5, time.Hour, nil)
	if _, err := p.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if workers.get(w.ID).DesiredState != domain.DesiredPaused {
		t.Fatal("OTP worker was restarted automatically after being skipped")
	}
}

func TestElasticPoolKeepsCompletedLoginTestWorkerOff(t *testing.T) {
	workers := newFakeWorkerStore()
	w := readyFixture(workerFixture("w1", domain.DesiredPaused, 1, intPtr(1)))
	w.Source = domain.SourceAuto
	w.LastError = strPtr("login_test_complete: AUTHENTICATED")
	workers.seed(w)
	p := NewElasticPool(workers, newFakeDriver(), fixedClock{t: time.Now()}, 5, time.Hour, nil)
	if _, err := p.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if workers.get(w.ID).DesiredState != domain.DesiredPaused {
		t.Fatal("completed login test worker was restarted")
	}
}

func TestManualRetryClearsOTPWorkerPauseReason(t *testing.T) {
	workers := newFakeWorkerStore()
	w := workerFixture("w1", domain.DesiredPaused, 1, intPtr(1))
	w.LastError = strPtr("otp_required: perlu OTP")
	workers.seed(w)
	workers.onReady = func(s *fakeWorkerStore, id string) { s.seed(readyFixture(s.get(id))) }
	waker := NewWaker(workers, newFakeDriver(), nil)
	if err := waker.Wake(context.Background(), w.ID); err != nil {
		t.Fatal(err)
	}
	if workers.get(w.ID).LastError != nil {
		t.Fatal("explicit retry must clear the old OTP shutdown reason")
	}
	if activity := workers.get(w.ID).LastActionAt; activity == nil || time.Since(*activity) > time.Minute {
		t.Fatal("wake must renew activity so the idle pool does not stop the booting browser")
	}
}
