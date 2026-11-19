package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
)

// fakeActionText is an in-memory port.ActionTextStore for the enqueue tests.
type fakeActionText struct {
	byJob map[string]string
}

func newFakeActionText() *fakeActionText { return &fakeActionText{byJob: map[string]string{}} }

func (f *fakeActionText) Put(_ context.Context, jobID, text string, _ time.Duration) error {
	f.byJob[jobID] = text
	return nil
}
func (f *fakeActionText) Get(_ context.Context, jobID string) (string, bool, error) {
	t, ok := f.byJob[jobID]
	return t, ok, nil
}

// A per-account comment body is stored under the created job id so the scheduler
// reads it at dispatch instead of composing from a template.
func TestActionEnqueueStoresPerAccountText(t *testing.T) {
	store := newActionStore()
	accs := &actionAccounts{acc: domain.Account{Platform: domain.PlatformInstagram, Status: domain.AccountActive, AuthStatus: domain.AuthAuthenticated}}
	texts := newFakeActionText()
	svc := NewActionService(store, accs, &actionTargets{ups: targetUpsert{id: "tgt-1"}}, fixedClock{}, nil, texts, nil, nil)

	jobs, err := svc.Enqueue(context.Background(), []ActionItem{
		{AccountID: "acct-1", TargetURL: "https://instagram.com/p/abc", Type: domain.JobTypeActionComment, Text: "halo dunia"},
	})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("want 1 job, got %d", len(jobs))
	}
	if got := texts.byJob[jobs[0].ID]; got != "halo dunia" {
		t.Fatalf("stored text = %q, want %q", got, "halo dunia")
	}
}

// Text on a like is rejected: a like posts nothing, so a body is a caller error.
func TestActionEnqueueRejectsTextOnLike(t *testing.T) {
	store := newActionStore()
	texts := newFakeActionText()
	svc := NewActionService(store, &actionAccounts{}, &actionTargets{ups: targetUpsert{id: "tgt-1"}}, fixedClock{}, nil, texts, nil, nil)

	_, err := svc.Enqueue(context.Background(), []ActionItem{
		{AccountID: "acct-1", TargetURL: "https://instagram.com/p/abc", Type: domain.JobTypeActionLike, Text: "nope"},
	})
	if !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("want ErrValidation for text on a like, got %v", err)
	}
}

// Text with no store wired is rejected rather than silently dropped.
func TestActionEnqueueRejectsTextWithoutStore(t *testing.T) {
	store := newActionStore()
	svc := NewActionService(store, &actionAccounts{}, &actionTargets{ups: targetUpsert{id: "tgt-1"}}, fixedClock{}, nil, nil, nil, nil)

	_, err := svc.Enqueue(context.Background(), []ActionItem{
		{AccountID: "acct-1", TargetURL: "https://instagram.com/p/abc", Type: domain.JobTypeActionComment, Text: "x"},
	})
	if !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("want ErrValidation when no text store, got %v", err)
	}
}

// An over-long body is rejected before it reaches the store.
func TestActionEnqueueRejectsTooLongText(t *testing.T) {
	store := newActionStore()
	svc := NewActionService(store, &actionAccounts{}, &actionTargets{ups: targetUpsert{id: "tgt-1"}}, fixedClock{}, nil, newFakeActionText(), nil, nil)

	_, err := svc.Enqueue(context.Background(), []ActionItem{
		{AccountID: "acct-1", TargetURL: "https://instagram.com/p/abc", Type: domain.JobTypeActionComment, Text: strings.Repeat("a", MaxCommentTextLen+1)},
	})
	if !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("want ErrValidation for over-long text, got %v", err)
	}
}

// --- M-3: a newline in the body is rejected, not silently posted in halves --

// The worker types the body with pressSequentially, where "\n" is an Enter
// keypress: "Great shot!\nDM me at foo@bar" submits "Great shot!", clears the
// composer, types the rest into the fresh one and abandons it — then reports
// SUCCESS with the full two-line string as renderedText, a string that was
// never posted. Rejected at the boundary instead.
func TestActionEnqueueRejectsNewlineInText(t *testing.T) {
	for name, body := range map[string]string{
		"newline":         "Great shot!\nDM me at foo@bar",
		"carriage return": "Great shot!\rDM me at foo@bar",
		"crlf":            "Great shot!\r\nDM me at foo@bar",
	} {
		t.Run(name, func(t *testing.T) {
			store := newActionStore()
			texts := newFakeActionText()
			svc := NewActionService(store, &actionAccounts{acc: domain.Account{Platform: domain.PlatformInstagram, Status: domain.AccountActive, AuthStatus: domain.AuthAuthenticated}}, &actionTargets{ups: targetUpsert{id: "tgt-1"}}, fixedClock{}, nil, texts, nil, nil)

			_, err := svc.Enqueue(context.Background(), []ActionItem{
				{AccountID: "acct-1", TargetURL: "https://instagram.com/p/abc", Type: domain.JobTypeActionComment, Text: body},
			})
			if !errors.Is(err, domain.ErrValidation) {
				t.Fatalf("want ErrValidation for %q, got %v", body, err)
			}
			// Nothing may have been written: the batch is all-or-nothing.
			if len(store.pending) != 0 || len(texts.byJob) != 0 {
				t.Fatalf("a rejected body must not be stored, jobs=%d texts=%v", len(store.pending), texts.byJob)
			}
		})
	}
}

// The rejection is for the newline, not for multi-word text: a one-line body
// still enqueues.
func TestActionEnqueueAcceptsSingleLineText(t *testing.T) {
	store := newActionStore()
	texts := newFakeActionText()
	svc := NewActionService(store, &actionAccounts{acc: domain.Account{Platform: domain.PlatformInstagram, Status: domain.AccountActive, AuthStatus: domain.AuthAuthenticated}}, &actionTargets{ups: targetUpsert{id: "tgt-1"}}, fixedClock{}, nil, texts, nil, nil)

	jobs, err := svc.Enqueue(context.Background(), []ActionItem{
		{AccountID: "acct-1", TargetURL: "https://instagram.com/p/abc", Type: domain.JobTypeActionComment, Text: "Great shot! DM me at foo@bar"},
	})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("want 1 job, got %d", len(jobs))
	}
}
