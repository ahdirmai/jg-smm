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

// TestLoginDeliversUnsealedCredentials proves the operator's "Log in" button
// carries the stored credentials: the auth-login control payload holds the
// username and the unsealed password (the same wire hop that carries 2FA
// codes via auth-input), while the row keeps holding only the sealed blob.
func TestLoginPreparesMissingLiveViewBeforeDispatch(t *testing.T) {
	accounts := newFakeAccountStore()
	workers := newFakeWorkerStore()
	w := workerFixture("w1", domain.DesiredPaused, 1, nil)
	workers.seed(w)
	workers.onReady = func(s *fakeWorkerStore, id string) {
		ready := readyFixture(s.get(id))
		ready.NoVNCService = strPtr("http://localhost:24105")
		s.seed(ready)
	}
	pub := &recordingPublisher{}
	svc := NewAccountService(accounts, workers, nil, AccountConfig{
		Control: pub, Waker: NewWaker(workers, newFakeDriver(), nil),
	})
	a := accounts.seedLockedForTest(domain.Account{Platform: domain.PlatformInstagram, Username: "legacy", WorkerID: &w.ID})
	if _, err := svc.Login(context.Background(), a.ID); err != nil {
		t.Fatalf("login must wake and prepare a legacy worker's live view: %v", err)
	}
	if pub.last() == nil {
		t.Fatal("login was not dispatched after live view became ready")
	}
}

func TestLoginDeliversUnsealedCredentials(t *testing.T) {
	accounts := newFakeAccountStore()
	workers := newFakeWorkerStore()
	pub := &recordingPublisher{}
	svc := NewAccountService(accounts, workers, nil, AccountConfig{Sealer: stubSealer{}, Control: pub})

	workerID := "worker-1"
	novnc := "http://127.0.0.1:24100"
	workers.seed(domain.Worker{ID: workerID, NoVNCService: &novnc})
	enc, err := stubSealer{}.Seal([]byte("hunter2"))
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	a := accounts.seedLockedForTest(domain.Account{
		Platform:    domain.PlatformInstagram,
		Username:    "growthco",
		PasswordEnc: enc,
		WorkerID:    &workerID,
	})

	view, err := svc.Login(context.Background(), a.ID)
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if view.AuthStatus != domain.AuthAuthenticating {
		t.Fatalf("authStatus = %s, want authenticating", view.AuthStatus)
	}
	raw := pub.last()
	if raw == nil {
		t.Fatal("login published no control message")
	}
	var msg struct {
		Type    string            `json:"type"`
		Payload map[string]string `json:"payload"`
	}
	if err := json.Unmarshal(raw, &msg); err != nil {
		t.Fatalf("unmarshal control: %v", err)
	}
	if msg.Type != "auth-login" {
		t.Fatalf("type = %q, want auth-login", msg.Type)
	}
	if msg.Payload["username"] != "growthco" || msg.Payload["password"] != "hunter2" {
		t.Fatalf("payload = %v, want the unsealed credentials", msg.Payload)
	}
}

// TestLoginWithoutStoredPasswordSendsNoPayload proves the manual path is
// unchanged: an account without a stored credential gets an empty payload, so
// the worker's autofill is skipped and the operator types in the live view.
func TestLoginWithoutStoredPasswordSendsNoPayload(t *testing.T) {
	accounts := newFakeAccountStore()
	workers := newFakeWorkerStore()
	pub := &recordingPublisher{}
	svc := NewAccountService(accounts, workers, nil, AccountConfig{Sealer: stubSealer{}, Control: pub})

	workerID := "worker-1"
	novnc := "http://127.0.0.1:24100"
	workers.seed(domain.Worker{ID: workerID, NoVNCService: &novnc})
	a := accounts.seedLockedForTest(domain.Account{
		Platform: domain.PlatformInstagram,
		Username: "growthco",
		WorkerID: &workerID,
	})

	if _, err := svc.Login(context.Background(), a.ID); err != nil {
		t.Fatalf("login: %v", err)
	}
	raw := pub.last()
	if raw == nil {
		t.Fatal("login published no control message")
	}
	var msg struct {
		Type    string            `json:"type"`
		Payload map[string]string `json:"payload"`
	}
	if err := json.Unmarshal(raw, &msg); err != nil {
		t.Fatalf("unmarshal control: %v", err)
	}
	if msg.Type != "auth-login" {
		t.Fatalf("type = %q, want auth-login", msg.Type)
	}
	if len(msg.Payload) != 0 {
		t.Fatalf("payload = %v, want empty (operator types manually)", msg.Payload)
	}
}

// fakeSessionStore is an in-memory port.BrowserSessionStore. It records the
// SEALED bytes, so a test can assert the plaintext cookie never reaches it.
type fakeSessionStore struct {
	mu      sync.Mutex
	states  map[string][]byte
	touched []string
}

func newFakeSessionStore() *fakeSessionStore {
	return &fakeSessionStore{states: map[string][]byte{}}
}

func (f *fakeSessionStore) GetState(_ context.Context, id string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.states[id], nil
}

func (f *fakeSessionStore) Save(_ context.Context, id string, sealed []byte, _ *time.Time, _ *string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.states[id] = append([]byte(nil), sealed...)
	return nil
}

func (f *fakeSessionStore) Touch(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.touched = append(f.touched, id)
	return nil
}

func (f *fakeSessionStore) Delete(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.states, id)
	return nil
}

func (f *fakeSessionStore) DueBefore(context.Context, time.Time, int) ([]string, error) {
	return nil, nil
}

// A session captured by one container must be handed back to the next one, so a
// rotating fleet does not pay for a login per swap. Two properties matter and
// both are asserted here: the cookie is stored SEALED (never plaintext at rest),
// and the next Login dispatch pushes it BEFORE the login message, unsealed, so
// the worker can hydrate and skip the form.
func TestLoginReimportsStoredSessionBeforeLoggingIn(t *testing.T) {
	accounts := newFakeAccountStore()
	workers := newFakeWorkerStore()
	pub := &recordingPublisher{}
	sessions := newFakeSessionStore()
	svc := NewAccountService(accounts, workers, nil, AccountConfig{
		Sealer: stubSealer{}, Control: pub, Sessions: sessions,
	})
	worker := "worker-1"
	novnc := "http://127.0.0.1:24100"
	workers.seed(domain.Worker{ID: worker, NoVNCService: &novnc})
	a := accounts.seedLockedForTest(domain.Account{
		Platform: domain.PlatformInstagram,
		Username: "growthco",
		WorkerID: &worker,
	})

	// Seed a captured session the way ExportSession would have stored it.
	const rawSession = `{"cookies":[{"name":"sessionid","value":"abc"}],"origins":[]}`
	if err := sessions.Save(context.Background(), a.ID, mustSeal(t, rawSession), nil, &worker); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	stored := sessions.states[a.ID]
	if string(stored) == rawSession {
		t.Fatal("session stored in PLAINTEXT: a session cookie is a bearer credential")
	}

	if _, err := svc.Login(context.Background(), a.ID); err != nil {
		t.Fatalf("login: %v", err)
	}

	pub.mu.Lock()
	msgs := append([][]byte(nil), pub.control...)
	pub.mu.Unlock()
	if len(msgs) < 2 {
		t.Fatalf("published %d control messages, want import then login", len(msgs))
	}
	var first struct {
		Type    string `json:"type"`
		Payload struct {
			Session json.RawMessage `json:"session"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(msgs[0], &first); err != nil {
		t.Fatalf("parse first message: %v", err)
	}
	if first.Type != "auth-import" {
		t.Fatalf("first control message type = %q, want auth-import (import must precede login)", first.Type)
	}
	if got := string(first.Payload.Session); got != rawSession {
		t.Errorf("imported session = %q, want the original cookie %q", got, rawSession)
	}
	var second struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(msgs[len(msgs)-1], &second); err != nil {
		t.Fatalf("parse last message: %v", err)
	}
	if second.Type != "auth-login" {
		t.Errorf("last control message type = %q, want auth-login", second.Type)
	}
	if len(sessions.touched) != 1 {
		t.Errorf("session touched %d times, want 1 (a reuse should be recorded)", len(sessions.touched))
	}
}

// An account that has never logged in must still log in normally: no stored
// session is the normal state, not an error that blocks the flow.
func TestLoginWithoutStoredSessionStillLogsIn(t *testing.T) {
	accounts := newFakeAccountStore()
	workers := newFakeWorkerStore()
	pub := &recordingPublisher{}
	svc := NewAccountService(accounts, workers, nil, AccountConfig{
		Sealer: stubSealer{}, Control: pub, Sessions: newFakeSessionStore(),
	})
	worker := "worker-1"
	novnc := "http://127.0.0.1:24100"
	workers.seed(domain.Worker{ID: worker, NoVNCService: &novnc})
	a := accounts.seedLockedForTest(domain.Account{
		Platform: domain.PlatformInstagram,
		Username: "freshacct",
		WorkerID: &worker,
	})
	if _, err := svc.Login(context.Background(), a.ID); err != nil {
		t.Fatalf("login on a fresh account: %v", err)
	}
	if pub.last() == nil {
		t.Fatal("no control message published for a fresh account")
	}
}

func mustSeal(t *testing.T, s string) []byte {
	t.Helper()
	b, err := stubSealer{}.Seal([]byte(s))
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	return b
}
