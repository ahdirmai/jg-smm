// Package workerrpc is the HTTPS push client: the API calls a worker, never the
// other way round for work.
//
// On boxd a worker sleeps when idle and wakes on an inbound request (~85ms from
// hibernation). That inverts the classic pull model — the always-on control
// plane does the waiting — and it is also why this client has a dial timeout
// long enough for a cold wake: the first packet is what starts the VM.
package workerrpc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm/apps/api/internal/port"
)

// SecretHeader is the machine credential every call carries. It is the only
// thing standing between a public boxd hostname and an authenticated browser.
const SecretHeader = "X-Internal-Secret"

// Defaults sized for boxd: a hibernated microVM takes ~85ms to answer its first
// packet, and the action dispatch itself only waits for the 202.
const (
	DefaultDialTimeout    = 10 * time.Second
	DefaultRequestTimeout = 30 * time.Second
)

// Client implements port.WorkerRPC over net/http.
type Client struct {
	http   *http.Client
	secret string
}

var _ port.WorkerRPC = (*Client)(nil)

// New builds the client. An empty secret is refused by every call rather than
// sent as an empty header: a fleet running without it would accept actions from
// anyone who can reach the worker hostname.
func New(secret string) *Client {
	transport := &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   DefaultDialTimeout,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		TLSHandshakeTimeout:   3 * DefaultDialTimeout,
		ResponseHeaderTimeout: DefaultRequestTimeout,
		MaxIdleConnsPerHost:   4,
		// A hibernated VM answers on the same host once warm; reusing the
		// connection is what makes the second call fast.
		IdleConnTimeout: 90 * time.Second,
	}
	return &Client{
		http:   &http.Client{Timeout: DefaultRequestTimeout, Transport: transport},
		secret: secret,
	}
}

// ExecuteAction pushes one action. The worker answers 202 on acceptance and
// reports the verdict through /internal/action-callback, so a 202 here is not a
// result — it is a promise the callback will carry.
func (c *Client) ExecuteAction(ctx context.Context, endpointURL string, req port.ExecuteActionRequest) error {
	return c.post(ctx, endpointURL, "/internal/execute-action", req)
}

// StartLogin opens the headful login context on the worker's display.
func (c *Client) StartLogin(ctx context.Context, endpointURL string, runtime port.AccountRuntimeSpec) error {
	return c.post(ctx, endpointURL, "/internal/start-login", runtime)
}

// SubmitAuthInput types a 2FA / checkpoint code into the parked context.
func (c *Client) SubmitAuthInput(ctx context.Context, endpointURL, accountID, platform, code string) error {
	return c.post(ctx, endpointURL, "/internal/auth-input", map[string]string{
		"accountId": accountID,
		"platform":  platform,
		"value":     code,
	})
}

// ClearAuth drops a parked login context so the display is free again.
func (c *Client) ClearAuth(ctx context.Context, endpointURL, accountID string) error {
	return c.post(ctx, endpointURL, "/internal/auth-clear", map[string]string{"accountId": accountID})
}

// OpenBrowser navigates the manual live-view context.
func (c *Client) OpenBrowser(ctx context.Context, endpointURL string, runtime port.AccountRuntimeSpec, targetURL string) error {
	return c.post(ctx, endpointURL, "/internal/browser-open", map[string]any{
		"runtime":   runtime,
		"targetUrl": targetURL,
	})
}

// PingKeepalive resets the VM's idle timer while an operator works in the live
// view: an inbound request counts as activity, so the microVM stays awake for
// the length of the CAPTCHA.
func (c *Client) PingKeepalive(ctx context.Context, endpointURL string) error {
	return c.post(ctx, endpointURL, "/internal/ping", nil)
}

// BusyAccount asks which account (if any) currently owns the worker's display.
func (c *Client) BusyAccount(ctx context.Context, endpointURL string) (string, error) {
	if endpointURL == "" {
		return "", fmt.Errorf("%w: worker has no endpoint url", domain.ErrUnavailable)
	}
	url := strings.TrimRight(endpointURL, "/") + "/healthz"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("workerrpc: build healthz: %w", err)
	}
	res, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("%w: healthz: %v", domain.ErrUnavailable, err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%w: healthz returned %d", domain.ErrUnavailable, res.StatusCode)
	}
	var out struct {
		AccountID *string `json:"accountId"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 4<<10)).Decode(&out); err != nil {
		return "", fmt.Errorf("workerrpc: decode healthz: %w", err)
	}
	if out.AccountID == nil {
		return "", nil
	}
	return *out.AccountID, nil
}

// post sends one JSON body and maps the worker's status onto a domain error.
//
// The body is never logged, at any level: every payload here carries either a
// password, a session cookie, or both.
func (c *Client) post(ctx context.Context, endpointURL, path string, body any) error {
	if c.secret == "" {
		return fmt.Errorf("%w: internal secret is not configured", domain.ErrUnavailable)
	}
	if endpointURL == "" {
		return fmt.Errorf("%w: worker has no endpoint url", domain.ErrUnavailable)
	}
	var payload io.Reader
	if body != nil {
		enc, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("workerrpc: marshal %s: %w", path, err)
		}
		payload = bytes.NewReader(enc)
	}
	url := strings.TrimRight(endpointURL, "/") + path
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, payload)
	if err != nil {
		return fmt.Errorf("workerrpc: build %s: %w", path, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(SecretHeader, c.secret)

	res, err := c.http.Do(req)
	if err != nil {
		// Network failure IS retryable: the VM may still be waking, or the
		// request landed on a machine that slept a heartbeat early.
		return fmt.Errorf("%w: %s: %v", domain.ErrUnavailable, path, err)
	}
	defer func() {
		// Drain so the connection can be reused; the body itself is ignored.
		_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 4<<10))
		_ = res.Body.Close()
	}()

	switch {
	case res.StatusCode >= 200 && res.StatusCode < 300:
		return nil
	case res.StatusCode == http.StatusConflict:
		return fmt.Errorf("%w: worker busy (%s)", domain.ErrConflict, path)
	case res.StatusCode == http.StatusUnauthorized || res.StatusCode == http.StatusForbidden:
		return fmt.Errorf("%w: worker rejected the internal secret (%s)", domain.ErrUnauthorized, path)
	case res.StatusCode == http.StatusBadRequest:
		return fmt.Errorf("%w: worker rejected %s", domain.ErrValidation, path)
	default:
		return fmt.Errorf("%w: %s returned %d", domain.ErrUnavailable, path, res.StatusCode)
	}
}

// IsRetryable reports whether a dispatch error deserves another attempt on the
// next tick. A conflict (worker busy) and an unavailable worker are transient;
// a rejected secret and a validation error are not.
func IsRetryable(err error) bool {
	return errors.Is(err, domain.ErrUnavailable) || errors.Is(err, domain.ErrConflict)
}
