package port

import (
	"context"
	"encoding/json"
)

// WorkerRPC is the push transport to a stateless worker (boxd microVM).
//
// Why push, not a Redis pull loop: a boxd VM only stops billing when it sleeps,
// and it can only sleep when nothing keeps an outbound connection busy. A worker
// that BLPOPs Redis never idles, and a hibernated worker cannot BLPOP at all. An
// inbound HTTPS request instead WAKES the VM (~85ms from hibernation), runs the
// action, and lets the idle timer put it back to sleep — so the API, which is
// always awake, does the waiting.
//
// Implementations must never log a request body: every call below carries either
// a credential, a session cookie, or both.
type WorkerRPC interface {
	// ExecuteAction dispatches one action. The worker answers 202 as soon as it
	// accepts the job and reports the verdict over /internal/action-callback,
	// so this returns on acceptance, not on completion.
	ExecuteAction(ctx context.Context, endpointURL string, req ExecuteActionRequest) error
	// StartLogin opens a headful login context on the worker's display.
	StartLogin(ctx context.Context, endpointURL string, runtime AccountRuntimeSpec) error
	// SubmitAuthInput types a 2FA / checkpoint code into the parked context.
	SubmitAuthInput(ctx context.Context, endpointURL, accountID, platform, code string) error
	// ClearAuth drops a parked login context and frees the display.
	ClearAuth(ctx context.Context, endpointURL, accountID string) error
	// OpenBrowser navigates a manual live-view context to targetURL.
	OpenBrowser(ctx context.Context, endpointURL string, runtime AccountRuntimeSpec, targetURL string) error
	// PingKeepalive is an inbound request whose only job is to reset the VM's
	// idle timer while an operator works through a CAPTCHA in the live view.
	PingKeepalive(ctx context.Context, endpointURL string) error
	// BusyAccount asks the worker which account owns its display ("" = idle).
	// Liveness alone cannot prove a parked login still exists: a healthy worker
	// with someone else's context, or none at all, answers fine.
	BusyAccount(ctx context.Context, endpointURL string) (string, error)
}

// AccountRuntimeSpec is everything a worker needs to build ONE isolated
// BrowserContext. It travels per call, never cached on the worker: the whole
// point of a stateless pool is that no account state outlives the request that
// carried it.
type AccountRuntimeSpec struct {
	AccountID string `json:"accountId"`
	Platform  string `json:"platform"`
	// StorageState is the plaintext Playwright storageState. It crosses exactly
	// one HTTPS hop and never reaches a log, a queue, or disk on the worker.
	StorageState json.RawMessage `json:"storageState,omitempty"`
	Credentials  *LoginCreds     `json:"credentials,omitempty"`
	// Geolocation pins the context's GPS to the account's city. The worker VM
	// sits in a datacentre; without this the page sees its real position.
	Geolocation *GeoPoint `json:"geolocation,omitempty"`
	// Timezone is the IANA zone matching that city (Asia/Jakarta, Asia/Makassar).
	// A Frankfurt clock beside a Jakarta coordinate is the anomaly fingerprint.
	Timezone  string     `json:"timezone"`
	UserAgent string     `json:"userAgent,omitempty"`
	Proxy     *ProxySpec `json:"proxy,omitempty"`
}

// LoginCreds is the password the login form needs. Plaintext: sealed at rest in
// the database, opened only to build this struct.
type LoginCreds struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// GeoPoint is a WGS-84 coordinate.
type GeoPoint struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
}

// ProxySpec is a per-context egress. Chromium (Playwright 1.48) accepts
// credentials at context level only for http/https, so a socks5 proxy with auth
// is rejected before it is ever dispatched.
type ProxySpec struct {
	// Server is scheme://host:port.
	Server   string `json:"server"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
}

// ExecuteActionRequest is one action job pushed to a worker.
type ExecuteActionRequest struct {
	JobID           string             `json:"jobId"`
	Action          string             `json:"action"`
	TargetURL       string             `json:"targetUrl"`
	Text            string             `json:"text,omitempty"`
	TemplateID      string             `json:"templateId,omitempty"`
	ExpectedMention string             `json:"expectedMention,omitempty"`
	Attempt         int                `json:"attempt"`
	Runtime         AccountRuntimeSpec `json:"runtime"`
}
