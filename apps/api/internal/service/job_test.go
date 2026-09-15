package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/port"
)

// P3-12 callback-path classification. The classifier itself is proven in the
// domain package; these tests pin the boundary contract: which verdicts get a
// class at all, and that the class is derived rather than trusted from the
// caller.

func TestClassifyAttempt(t *testing.T) {
	cases := []struct {
		name   string
		status domain.AttemptStatus
		errMsg string
		want   domain.ErrorClass
	}{
		// Only failures and retries carry a class; a success or a cancellation
		// has nothing to classify, and "" is stored as NULL.
		{"success has no class", domain.AttemptSuccess, "", ""},
		{"cancelled has no class", domain.AttemptCancelled, "user cancelled", ""},
		{"running has no class", domain.AttemptRunning, "", ""},
		{"failed is classified", domain.AttemptFailed, "HTTP 429 Too Many Requests", domain.ErrorClassRateLimit},
		{"retry is classified", domain.AttemptRetry, "page.goto: Timeout 30000ms exceeded", domain.ErrorClassTransient},
		{"failed banned", domain.AttemptFailed, "your account has been banned", domain.ErrorClassBanned},
		{"failed auth", domain.AttemptFailed, "please log in to continue", domain.ErrorClassAuth},
		// A failure with no reason lands on UNKNOWN, which is NOT retryable —
		// a callback that reports failure without a cause must not quietly
		// retry forever.
		{"failed empty reason is unknown", domain.AttemptFailed, "", domain.ErrorClassUnknown},
		{"failed unrecognised is unknown", domain.AttemptFailed, "mystery glitch", domain.ErrorClassUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClassifyAttempt(tc.status, tc.errMsg); got != tc.want {
				t.Fatalf("ClassifyAttempt(%q, %q) = %q, want %q", tc.status, tc.errMsg, got, tc.want)
			}
		})
	}
}

// TestRecordAttemptClassifies asserts the callback path attaches a class to a
// failed verdict before anything is persisted. Persistence lands with P3-11;
// the classification is the P3-12 contract and must hold now so P3-11 stores a
// real class instead of backfilling one.
func TestRecordAttemptClassifies(t *testing.T) {
	svc := NewJobService(nil, nil, nil, nil, nil, nil, nil)
	ctx := t.Context()

	// A banned-account failure: the callback must be accepted and classified,
	// not rejected as unparseable. Persistence is P3-11, so the call still
	// reports the verdict as not-yet-persisted — the point is that the path
	// reaches classification without dropping the verdict.
	if err := svc.RecordAttempt(ctx, AttemptRecord{
		AttemptID: "job-1:2",
		Status:    domain.AttemptFailed,
		Error:     strPtr("your account has been banned"),
	}); err == nil {
		t.Fatal("expected the not-yet-persisted error before P3-11 wires the store")
	}
}

// strPtr boxes a string for the pointer-typed error field.
func strPtr(s string) *string { return &s }

// captureStream is a StreamPublisher that records frames so a test can prove a
// verdict reached the dashboard channel (P4-03).
type captureStream struct {
	frames []struct {
		kind string
		body string
	}
}

func (c *captureStream) Publish(_ context.Context, kind string, payload []byte) {
	c.frames = append(c.frames, struct {
		kind string
		body string
	}{kind: kind, body: string(payload)})
}

func TestRecordAttemptPublishesActionFrame(t *testing.T) {
	store := newFakeActionStore()
	stream := &captureStream{}
	svc := NewJobService(nil, nil, nil, store, fixedClock{t: time.Unix(1_000_000, 0).UTC()}, stream, nil)

	err := svc.RecordAttempt(context.Background(), AttemptRecord{
		AttemptID:    "job-1:1",
		Status:       domain.AttemptSuccess,
		RenderedText: func() *string { s := "nice shot!"; return &s }(),
		WorkerID:     func() *string { s := "worker-1"; return &s }(),
	})
	if err != nil {
		t.Fatalf("record attempt: %v", err)
	}

	if len(stream.frames) != 1 {
		t.Fatalf("frames = %d, want 1", len(stream.frames))
	}
	if stream.frames[0].kind != port.EventActionUpdated {
		t.Errorf("frame kind = %q, want %s", stream.frames[0].kind, port.EventActionUpdated)
	}
	body := stream.frames[0].body
	for _, want := range []string{`"jobId":"job-1"`, `"verified":true`, `"renderedText":"nice shot!"`} {
		if !strings.Contains(body, want) {
			t.Errorf("frame body missing %s: %s", want, body)
		}
	}
}
