package domain

import "testing"

func TestEnumValidity(t *testing.T) {
	cases := []struct {
		name string
		ok   bool
	}{
		{"auth", AuthAuthenticated.Valid()},
		{"auth-bad", !AuthStatus("nope").Valid()},
		{"account", AccountActive.Valid()},
		{"account-bad", !AccountStatus("").Valid()},
		{"worker", WorkerIdle.Valid()},
		{"worker-bad", !WorkerStatus("zombie").Valid()},
		{"desired", DesiredRunning.Valid() && DesiredStopped.Valid()},
		{"desired-bad", !DesiredState("SLEEP").Valid()},
	}
	for _, c := range cases {
		if !c.ok {
			t.Errorf("%s: expected validity check to hold", c.name)
		}
	}
}

func TestChannelAndResourceNaming(t *testing.T) {
	const id = "abc123"
	if got := ControlChannel(id); got != "control-abc123" {
		t.Errorf("ControlChannel = %q", got)
	}
	if got := ActionQueue(id); got != "queue:action:abc123" {
		t.Errorf("ActionQueue = %q", got)
	}
	if got := SessionPVCName(id); got != "smm-session-abc123" {
		t.Errorf("SessionPVCName = %q", got)
	}
}

func TestAccountIsPackable(t *testing.T) {
	// P5-02: QUARANTINED is a holding state out of the pool. If it were packable,
	// the health model's auto-quarantine would be undone by the very next pack.
	packable := []AccountStatus{AccountPending, AccountActive, AccountPaused}
	for _, s := range packable {
		if !(Account{Status: s}).IsPackable() {
			t.Errorf("status %s should be packable", s)
		}
	}
	for _, s := range []AccountStatus{AccountArchived, AccountDead, AccountQuarantined} {
		if (Account{Status: s}).IsPackable() {
			t.Errorf("status %s should not be packable", s)
		}
	}
}
