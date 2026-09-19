package service

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
)

// recordingPublisher is a port.Publisher that captures the control messages the
// account service publishes, so a test can assert the wire shape (and pull the
// generated requestId back out for the export round trip).
type recordingPublisher struct {
	mu      sync.Mutex
	control [][]byte
	err     error
}

func (p *recordingPublisher) Enqueue(_ context.Context, _ string, _ []byte) error { return nil }

func (p *recordingPublisher) PublishControl(_ context.Context, _ string, msg []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.err != nil {
		return p.err
	}
	// Copy: the caller may reuse the buffer.
	cp := append([]byte(nil), msg...)
	p.control = append(p.control, cp)
	return nil
}

func (p *recordingPublisher) last() []byte {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.control) == 0 {
		return nil
	}
	return p.control[len(p.control)-1]
}

func seededExportAccount(t *testing.T) (*AccountService, *recordingPublisher, string) {
	t.Helper()
	accounts := newFakeAccountStore()
	workers := newFakeWorkerStore()
	pub := &recordingPublisher{}
	svc := NewAccountService(accounts, workers, nil, AccountConfig{Sealer: stubSealer{}, Control: pub})
	worker := "worker-1"
	a := accounts.seedLockedForTest(domain.Account{
		Platform: domain.PlatformInstagram,
		Username: "growthco",
		WorkerID: &worker,
	})
	return svc, pub, a.ID
}

// seedLockedForTest exposes the fake store's seed under lock for this file.
func (s *fakeAccountStore) seedLockedForTest(a domain.Account) domain.Account {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.seedLocked(a)
}

// TestExportSessionRoundTrip proves the export flow: the service publishes an
// auth-export control message carrying a requestId, and DeliverExportedSession
// (the worker callback) hands the session back to the waiting caller.
func TestExportSessionRoundTrip(t *testing.T) {
	svc, pub, id := seededExportAccount(t)
	want := json.RawMessage(`{"cookies":[{"name":"sessionid","value":"x"}]}`)

	type result struct {
		session json.RawMessage
		err     error
	}
	done := make(chan result, 1)
	go func() {
		s, err := svc.ExportSession(context.Background(), id)
		done <- result{s, err}
	}()

	// Wait for the control message, then extract the generated requestId.
	var requestID string
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if raw := pub.last(); raw != nil {
			var msg struct {
				Type    string `json:"type"`
				Payload struct {
					RequestID string `json:"requestId"`
				} `json:"payload"`
			}
			if err := json.Unmarshal(raw, &msg); err == nil && msg.Type == "auth-export" {
				requestID = msg.Payload.RequestID
				break
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	if requestID == "" {
		t.Fatal("no auth-export control message with a requestId was published")
	}

	svc.DeliverExportedSession(requestID, want)

	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("export: %v", r.err)
		}
		if string(r.session) != string(want) {
			t.Fatalf("session = %s, want %s", r.session, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("export did not return after delivery")
	}
}

// TestExportSessionNoSession asserts a worker that reports no session (empty
// delivery) turns into an honest not-found, not a hang or an empty success.
func TestExportSessionNoSession(t *testing.T) {
	svc, pub, id := seededExportAccount(t)
	done := make(chan error, 1)
	go func() {
		_, err := svc.ExportSession(context.Background(), id)
		done <- err
	}()

	var requestID string
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && requestID == "" {
		if raw := pub.last(); raw != nil {
			var msg struct {
				Payload struct {
					RequestID string `json:"requestId"`
				} `json:"payload"`
			}
			_ = json.Unmarshal(raw, &msg)
			requestID = msg.Payload.RequestID
		}
		time.Sleep(5 * time.Millisecond)
	}
	svc.DeliverExportedSession(requestID, nil) // no session on the container

	select {
	case err := <-done:
		if !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("want ErrNotFound for an empty session, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("export did not return after empty delivery")
	}
}

// TestImportSessionValidatesObject asserts a valid session object is published
// as an auth-import control message, and a non-object payload is rejected before
// anything is published.
func TestImportSessionValidatesObject(t *testing.T) {
	svc, pub, id := seededExportAccount(t)

	// A JSON array is not a storageState: reject it, publish nothing.
	if err := svc.ImportSession(context.Background(), id, json.RawMessage(`["not","an","object"]`)); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("want ErrValidation for a non-object session, got %v", err)
	}
	if pub.last() != nil {
		t.Fatal("a rejected import must not publish a control message")
	}

	// A valid object is published as auth-import carrying the session.
	session := json.RawMessage(`{"cookies":[{"name":"sessionid","value":"y"}]}`)
	if err := svc.ImportSession(context.Background(), id, session); err != nil {
		t.Fatalf("import: %v", err)
	}
	raw := pub.last()
	if raw == nil {
		t.Fatal("import published no control message")
	}
	var msg struct {
		Type    string `json:"type"`
		Payload struct {
			Session json.RawMessage `json:"session"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(raw, &msg); err != nil {
		t.Fatalf("unmarshal control: %v", err)
	}
	if msg.Type != "auth-import" {
		t.Fatalf("type = %q, want auth-import", msg.Type)
	}
	if string(msg.Payload.Session) != string(session) {
		t.Fatalf("session = %s, want %s", msg.Payload.Session, session)
	}
}
