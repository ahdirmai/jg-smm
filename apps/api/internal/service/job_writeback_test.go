package service

import (
	"context"
	"testing"
	"time"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm/apps/api/internal/port"
)

// A successful action rotates the platform's session token, so the state the
// worker captured is strictly newer than the one the dispatch sent. Losing it
// would strand the account on a cookie the platform already replaced — the
// account keeps working until that cookie dies, then every action fails at once.
func TestRecordAttemptWritesBackRotatedSession(t *testing.T) {
	workers := newFakeWorkerStore()
	accounts := newFakeAccountStore()
	accounts.seed(accountFixture("acct-1", domain.PlatformInstagram))
	actions := newFakeActionStore()
	actions.pending = append(actions.pending, domain.ActionJob{
		ID: "job-1", AccountID: "acct-1", Status: domain.JobStatusRunning,
	})
	sessions := newFakeSessionStore()
	sealer := &plainSealer{}

	jobs := NewJobService(workers, accounts, nil, actions, fixedClock{t: time.Now()}, nil, nil, nil).
		WithPool(PoolJobDeps{Mode: "stateless", Sessions: sessions, Sealer: sealer})

	rotated := []byte(`{"cookies":[{"name":"sessionid","value":"rotated"}]}`)
	err := jobs.RecordAttempt(context.Background(), AttemptRecord{
		AttemptID:      "job-1:1",
		Status:         domain.AttemptSuccess,
		UpdatedSession: rotated,
		WorkerID:       new("smm-worker-01"),
	})
	if err != nil {
		t.Fatalf("record attempt: %v", err)
	}

	stored, err := sessions.GetState(context.Background(), "acct-1")
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(stored) != string(rotated) {
		t.Fatalf("session not written back: got %q", stored)
	}
}

// Only a PROVED attempt may replace the stored cookie. A failed attempt is
// evidence that something went wrong, not that the session is fresher.
func TestRecordAttemptIgnoresSessionOnFailure(t *testing.T) {
	workers := newFakeWorkerStore()
	accounts := newFakeAccountStore()
	accounts.seed(accountFixture("acct-1", domain.PlatformInstagram))
	actions := newFakeActionStore()
	actions.pending = append(actions.pending, domain.ActionJob{
		ID: "job-1", AccountID: "acct-1", Status: domain.JobStatusRunning,
	})
	sessions := newFakeSessionStore()

	jobs := NewJobService(workers, accounts, nil, actions, fixedClock{t: time.Now()}, nil, nil, nil).
		WithPool(PoolJobDeps{Mode: "stateless", Sessions: sessions, Sealer: &plainSealer{}})

	reason := "target not found"
	err := jobs.RecordAttempt(context.Background(), AttemptRecord{
		AttemptID:      "job-1:1",
		Status:         domain.AttemptFailed,
		Error:          &reason,
		UpdatedSession: []byte(`{"cookies":[]}`),
	})
	if err != nil {
		t.Fatalf("record attempt: %v", err)
	}
	if stored, _ := sessions.GetState(context.Background(), "acct-1"); stored != nil {
		t.Fatalf("a failed attempt must not replace the session: %q", stored)
	}
}

// The pool leases are held until the job can make no further progress. A
// RUNNING report is the worker saying "still working", so freeing the display
// there would let a second job land on it.
func TestRecordAttemptReleasesLeasesOnlyWhenTerminal(t *testing.T) {
	workers := newFakeWorkerStore()
	accounts := newFakeAccountStore()
	accounts.seed(accountFixture("acct-1", domain.PlatformInstagram))
	actions := newFakeActionStore()
	actions.pending = append(actions.pending, domain.ActionJob{
		ID: "job-1", AccountID: "acct-1", Status: domain.JobStatusRunning,
	})
	var released []string
	jobs := NewJobService(workers, accounts, nil, actions, fixedClock{t: time.Now()}, nil, nil, nil).
		WithPool(PoolJobDeps{
			Mode:      "stateless",
			OnJobDone: func(jobID string) { released = append(released, jobID) },
		})

	if err := jobs.RecordAttempt(context.Background(), AttemptRecord{
		AttemptID: "job-1:1", Status: domain.AttemptRunning,
	}); err != nil {
		t.Fatalf("running report: %v", err)
	}
	if len(released) != 0 {
		t.Fatalf("a RUNNING report must keep the lease, released %v", released)
	}

	if err := jobs.RecordAttempt(context.Background(), AttemptRecord{
		AttemptID: "job-1:2", Status: domain.AttemptSuccess,
	}); err != nil {
		t.Fatalf("terminal report: %v", err)
	}
	if len(released) != 1 || released[0] != "job-1" {
		t.Fatalf("a terminal report must release the lease, got %v", released)
	}
}

// plainSealer is seam-only: it stores the bytes as-is so a test can read the
// write-back without standing up AES.
type plainSealer struct{}

func (plainSealer) Seal(p []byte) ([]byte, error) { return p, nil }
func (plainSealer) Open(c []byte) ([]byte, error) { return c, nil }

var _ port.Sealer = plainSealer{}

// F5: evidence must land in the SCREENSHOTS store under the bare file name.
// Writing "screenshots/<name>" into the raw-scrape bucket produced action_log
// rows pointing at objects no reader serves.
func TestRecordAttemptStoresEvidenceInScreenshotsStore(t *testing.T) {
	workers := newFakeWorkerStore()
	accounts := newFakeAccountStore()
	accounts.seed(accountFixture("acct-1", domain.PlatformInstagram))
	actions := newFakeActionStore()
	actions.pending = append(actions.pending, domain.ActionJob{
		ID: "job-1", AccountID: "acct-1", Status: domain.JobStatusRunning,
	})
	blobs := &recordingBlobs{}
	jobs := NewJobService(workers, accounts, nil, actions, fixedClock{t: time.Now()}, nil, nil, nil).
		WithPool(PoolJobDeps{Mode: "stateless", Blobs: blobs})

	err := jobs.RecordAttempt(context.Background(), AttemptRecord{
		AttemptID:       "job-1:1",
		Status:          domain.AttemptSuccess,
		Screenshot:      "task-job-1-smm-worker-01.jpg",
		ScreenshotBytes: []byte{0xff, 0xd8, 0xff},
	})
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	if len(blobs.keys) != 1 {
		t.Fatalf("uploaded %d objects, want 1", len(blobs.keys))
	}
	if want := "task-job-1-smm-worker-01.jpg"; blobs.keys[0] != want {
		t.Fatalf("key = %q, want the bare name %q (bucket is the namespace)", blobs.keys[0], want)
	}
}

// recordingBlobs captures what the evidence path uploads.
type recordingBlobs struct {
	keys []string
	data map[string][]byte
}

func (r *recordingBlobs) Put(_ context.Context, key string, payload []byte) (int64, error) {
	r.keys = append(r.keys, key)
	if r.data == nil {
		r.data = map[string][]byte{}
	}
	r.data[key] = payload
	return int64(len(payload)), nil
}

func (r *recordingBlobs) Get(context.Context, string) ([]byte, error) { return nil, nil }
