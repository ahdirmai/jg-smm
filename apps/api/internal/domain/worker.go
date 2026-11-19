package domain

import "time"

// Account is a worker account: an executor identity the worker logs into and
// performs Playwright actions with. It is NOT an OfficialAccount (a monitored,
// read-only brand account). PasswordEnc is write-only ciphertext and must never
// be surfaced through the API.
type Account struct {
	ID           string
	Platform     Platform
	Username     string
	PasswordEnc  []byte // AES-256-GCM ciphertext; never logged or returned.
	AuthStatus   AuthStatus
	Handle       *string
	LastVerified *time.Time
	ProxyGroupID *string
	HealthScore  int
	Status       AccountStatus
	Tags         []string
	WorkerID     *string // nil = not yet packed into a container.
	LastUsedAt   *time.Time
	LastChecked  *time.Time
	LastError    *string
	// CookieExpiryAt is when the session cookie dies, as reported by the last
	// verified login (P2-06). Nil means the platform dated nothing (a session
	// cookie), so the refresh cannot be scheduled — such an account is
	// refreshed by hand only.
	CookieExpiryAt *time.Time
	// RefreshFailures counts CONSECUTIVE failed session refreshes. Reset to 0
	// by any verified login; at the threshold the account is quarantined.
	RefreshFailures int
	CreatedAt       time.Time

	// Location, Latitude, Longitude and Timezone are the account's OWN device
	// identity. A boxd worker is a disposable microVM that wakes anywhere in the
	// region and sleeps again, so the city can no longer be read off the machine
	// the account happens to run on. The three location fields are set together
	// or not at all (DB constraint account_location_coords_pairing).
	Location  *string
	Latitude  *float64
	Longitude *float64
	// Timezone is the IANA zone the browser context must report (Asia/Jakarta,
	// Asia/Makassar). Never empty: the column defaults to WIB.
	Timezone string
	// UserAgent is the UA the context presents, or nil to use the platform
	// default. Stored so the fingerprint survives a worker swap unchanged.
	UserAgent *string
}

// IsPackable reports whether the account can be assigned to a container. A
// QUARANTINED account (P5-02 auto-quarantine below the health threshold, or an
// operator hold) stays out of the pool on purpose: packing it would immediately
// re-use the account the health model just pulled out.
func (a Account) IsPackable() bool {
	switch a.Status {
	case AccountArchived, AccountDead, AccountQuarantined:
		return false
	default:
		return true
	}
}

// ProxyGroup is a residential proxy pool bound to accounts at region level.
type ProxyGroup struct {
	ID             string
	Name           string
	Region         string // ISO 3166-1 alpha-2.
	Provider       string
	PoolKeyEnc     []byte // encrypted at rest.
	MaxConcurrency int
	DailyBudgetMB  int
	CreatedAt      time.Time
}

// Worker is one execution slot. In the stateless pool each row is a boxd
// microVM (smm-worker-01..10) that runs ONE account at a time: accounts reach it
// by HTTPS push and account.worker_id is a short lease, not a permanent home.
type Worker struct {
	ID             string
	Name           string
	ContainerID    *string
	ControlChannel *string
	ActionQueue    *string
	SessionPVC     *string
	NoVNCService   *string
	DesiredState   DesiredState
	Source         WorkerSource
	Region         string
	Location       *string
	Latitude       *float64
	Longitude      *float64
	Status         WorkerStatus
	Generation     int
	ObservedGen    *int
	ProvisionErr   *string
	BrowserStatus  string
	CurrentJobID   *string
	LastHeartbeat  *time.Time
	LastActionAt   *time.Time
	LastError      *string
	QueueDepth     int
	RestartCount   int
	ImageVersion   string
	CreatedAt      time.Time
	// EndpointURL is the worker's RPC base the API pushes actions to
	// (https://rpc.smm-worker-01.boxd.sh in the fleet,
	// http://smm-worker-01:8090 on a local compose network). The code is
	// identical in both; only this value differs.
	EndpointURL *string
}

// Heartbeat is one worker telemetry sample.
type Heartbeat struct {
	ID       string
	WorkerID string
	TS       time.Time
	CPU      float64
	Mem      float64
	JobsDone int
}

// ProvisionLog records a single provisioner operation for audit.
type ProvisionLog struct {
	ID         string
	WorkerID   string
	Op         ProvisionOp
	Generation int
	K8sRef     *string
	Status     ProvisionStatus
	Error      *string
	TS         time.Time
}

// ControlChannel returns the Redis pub/sub channel for a worker.
func ControlChannel(workerID string) string { return "control-" + workerID }

// ActionQueue returns the Redis durable queue key for a worker.
func ActionQueue(workerID string) string { return "queue:action:" + workerID }

// SessionPVCName returns the PVC name holding a worker's per-platform sessions.
func SessionPVCName(workerID string) string { return "smm-session-" + workerID }
