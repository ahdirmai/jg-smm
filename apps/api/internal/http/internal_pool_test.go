package http

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm/apps/api/internal/port"
	"github.com/ahdirmai/jg-smm/apps/api/internal/service"
)

// --- pool fakes ------------------------------------------------------------

type recordingBlobs struct {
	mu   map[string][]byte
	keys []string
}

func (r *recordingBlobs) Put(_ context.Context, key string, payload []byte) (int64, error) {
	r.keys = append(r.keys, key)
	if r.mu == nil {
		r.mu = map[string][]byte{}
	}
	r.mu[key] = payload
	return int64(len(payload)), nil
}

func (r *recordingBlobs) Get(context.Context, string) ([]byte, error) { return nil, nil }

// --- a snapshot of what a real callback carries ----------------------------

// A stateless worker has nowhere else to put its evidence: the same request
// carries the rotated storageState AND the JPEG. At the old 64 KiB cap this
// body was a 413 the worker could not diagnose, which stranded every session at
// its pre-action cookie.
func TestActionCallbackCarriesSessionAndEvidence(t *testing.T) {
	workers := newMemWorkerStoreForHTTP()
	accounts := newMemAccountStoreForHTTP()
	_, _ = accounts.Create(context.Background(), domain.Account{
		ID: "acct-1", Platform: domain.PlatformInstagram, Username: "u1", Status: domain.AccountActive,
	})
	jobs := service.NewJobService(workers, accounts, nil, nil, stubClock{t: time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)}, nil, nil, nil)
	blobs := &recordingBlobs{}
	jobs.WithPool(service.PoolJobDeps{Mode: "stateless", Blobs: blobs})
	h := NewInternalHandler(jobs, "")

	// ~350 KiB session: Instagram's storageState with a dozen origins.
	big := make([]byte, 350<<10)
	_, _ = rand.Read(big)
	session := map[string]any{
		"cookies": []any{map[string]any{"name": "sessionid", "value": base64.StdEncoding.EncodeToString(big)}},
		"origins": []any{},
	}
	jpeg := make([]byte, 120<<10)
	_, _ = rand.Read(jpeg)
	body, err := json.Marshal(map[string]any{
		"attemptId":           "job-1:1",
		"status":              "SUCCESS",
		"workerId":            "w1",
		"screenshotPath":      "task-job-1-smm-worker-01.jpg",
		"updated_session":     session,
		"screenshot_jpeg_b64": base64.StdEncoding.EncodeToString(jpeg),
		"durationMs":          1200,
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if len(body) >= 64<<10 {
		t.Logf("body is %d bytes, well past the old 64 KiB cap", len(body))
	}

	e := echo.New()
	e.HideBanner = true
	h.Register(e)
	req := httptest.NewRequest(http.MethodPost, "/internal/action-callback", bytes.NewReader(body))
	req.Header.Set(echo.HeaderContentType, "application/json")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	// The action store is nil in this fixture, so the service answers 202
	// ("accepted, persistence unavailable"). What matters here is that the
	// transport layer did not reject the body: 413 would be the old failure.
	if rec.Code == http.StatusRequestEntityTooLarge {
		t.Fatalf("callback rejected with 413: the body limit is below a real payload")
	}
	if rec.Code != http.StatusAccepted && rec.Code != http.StatusOK {
		t.Fatalf("code = %d body = %s", rec.Code, rec.Body.String())
	}
}

// Absent evidence is normal; malformed base64 is a 400 the worker can see.
func TestScreenshotBase64Validation(t *testing.T) {
	h, _, _, _ := newTestInternal()

	code, _ := call(h, http.MethodPost, "/internal/action-callback",
		`{"attemptId":"job-1:1","status":"SUCCESS","screenshot_jpeg_b64":"not base64!!"}`)
	if code != http.StatusBadRequest {
		t.Fatalf("malformed base64: code = %d, want 400", code)
	}

	// Absent is fine and must reach the service (which then reports the missing
	// action store as 202/503, not a validation error).
	if _, err := decodeScreenshot(nil); err != nil {
		t.Fatalf("nil evidence: %v", err)
	}
	empty := ""
	if b, err := decodeScreenshot(&empty); err != nil || b != nil {
		t.Fatalf("empty evidence: %v %v", b, err)
	}
}

func TestRawSessionRoundTrip(t *testing.T) {
	if raw := rawSession(nil); raw != nil {
		t.Fatalf("nil session must stay nil")
	}
	empty := map[string]any{}
	if raw := rawSession(&empty); raw != nil {
		t.Fatalf("empty session must stay nil")
	}
	one := map[string]any{"cookies": []any{}}
	raw := rawSession(&one)
	var back map[string]any
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("re-encoded session is not JSON: %v", err)
	}
	if _, ok := back["cookies"]; !ok {
		t.Fatalf("session content lost: %s", raw)
	}
}

var _ port.RawStorage = (*recordingBlobs)(nil)
