package workerrpc

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm/apps/api/internal/port"
)

// The client maps the worker's status onto the sentinels the scheduler branches
// on: 409 (busy) must be a conflict it can retry, 401 a permanent auth failure.
func TestStatusMapping(t *testing.T) {
	cases := []struct {
		status int
		want   error
	}{
		{http.StatusAccepted, nil},
		{http.StatusOK, nil},
		{http.StatusConflict, domain.ErrConflict},
		{http.StatusUnauthorized, domain.ErrUnauthorized},
		{http.StatusForbidden, domain.ErrUnauthorized},
		{http.StatusBadRequest, domain.ErrValidation},
		{http.StatusBadGateway, domain.ErrUnavailable},
		{http.StatusServiceUnavailable, domain.ErrUnavailable},
	}
	for _, tc := range cases {
		t.Run(http.StatusText(tc.status), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
			}))
			defer srv.Close()

			err := New("secret").ExecuteAction(t.Context(), srv.URL, port.ExecuteActionRequest{JobID: "j1"})
			if tc.want == nil {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

// Every call carries the secret and nothing else identifies the caller.
func TestCallsCarrySecretAndPath(t *testing.T) {
	var gotPath, gotSecret, gotCT string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotSecret = r.Header.Get(SecretHeader)
		gotCT = r.Header.Get("Content-Type")
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	c := New("s3cr3t")
	if err := c.ExecuteAction(t.Context(), srv.URL+"/", port.ExecuteActionRequest{JobID: "j1"}); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if gotPath != "/internal/execute-action" {
		t.Fatalf("path = %q", gotPath)
	}
	if gotSecret != "s3cr3t" {
		t.Fatalf("secret header = %q", gotSecret)
	}
	if gotCT != "application/json" {
		t.Fatalf("content-type = %q", gotCT)
	}

	if err := c.ClearAuth(t.Context(), srv.URL, "acct"); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if gotPath != "/internal/auth-clear" {
		t.Fatalf("path = %q", gotPath)
	}
	if err := c.PingKeepalive(t.Context(), srv.URL); err != nil {
		t.Fatalf("ping: %v", err)
	}
	if gotPath != "/internal/ping" {
		t.Fatalf("path = %q", gotPath)
	}
}

// A fleet without the secret must refuse to dispatch rather than send an empty
// header a worker would have to interpret.
func TestMissingSecretRefuses(t *testing.T) {
	err := New("").ExecuteAction(t.Context(), "http://127.0.0.1:1", port.ExecuteActionRequest{})
	if !errors.Is(err, domain.ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
	if err := New("s").ExecuteAction(t.Context(), "", port.ExecuteActionRequest{}); !errors.Is(err, domain.ErrUnavailable) {
		t.Fatalf("empty endpoint: err = %v, want ErrUnavailable", err)
	}
}

// A dead endpoint is retryable (the VM may be waking), not terminal.
func TestNetworkFailureIsRetryable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close()

	err := New("s").ExecuteAction(t.Context(), url, port.ExecuteActionRequest{})
	if !errors.Is(err, domain.ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
	if !IsRetryable(err) {
		t.Fatalf("a cold-wake failure must be retryable")
	}
}

// A recovery clear must be able to name NO account.
//
// The broken state is "this worker says busy but the lease that named the
// account is gone" — sending an empty id is the only way to free the display
// without restarting the machine.
func TestClearAuthSendsEmptyAccountID(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		gotBody = string(buf)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	if err := New("s3cret").ClearAuth(t.Context(), srv.URL, ""); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if !strings.Contains(gotBody, `"accountId":""`) {
		t.Fatalf("body must carry an empty accountId, got %q", gotBody)
	}
}
