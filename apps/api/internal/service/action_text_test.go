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
	accs := &actionAccounts{}
	texts := newFakeActionText()
	svc := NewActionService(store, accs, &actionTargets{ups: targetUpsert{id: "tgt-1"}}, fixedClock{}, nil, texts)

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
	svc := NewActionService(store, &actionAccounts{}, &actionTargets{ups: targetUpsert{id: "tgt-1"}}, fixedClock{}, nil, texts)

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
	svc := NewActionService(store, &actionAccounts{}, &actionTargets{ups: targetUpsert{id: "tgt-1"}}, fixedClock{}, nil, nil)

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
	svc := NewActionService(store, &actionAccounts{}, &actionTargets{ups: targetUpsert{id: "tgt-1"}}, fixedClock{}, nil, newFakeActionText())

	_, err := svc.Enqueue(context.Background(), []ActionItem{
		{AccountID: "acct-1", TargetURL: "https://instagram.com/p/abc", Type: domain.JobTypeActionComment, Text: strings.Repeat("a", MaxCommentTextLen+1)},
	})
	if !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("want ErrValidation for over-long text, got %v", err)
	}
}
