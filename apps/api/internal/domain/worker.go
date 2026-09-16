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
	CreatedAt    time.Time
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

// Worker is one container / one "device". It hosts many accounts, at most one
// per platform (enforced by the account unique constraint at the DB level).
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
