package domain

// AuthStatus is the login state machine for a worker account. It is deliberately
// separate from AccountStatus (the lifecycle state): an account can be ACTIVE in
// lifecycle terms while AUTHENTICATING after a re-login.
type AuthStatus string

const (
	AuthAuthenticating AuthStatus = "AUTHENTICATING"
	AuthNeedsInput     AuthStatus = "NEEDS_INPUT"
	AuthAuthenticated  AuthStatus = "AUTHENTICATED"
	AuthFailed         AuthStatus = "FAILED"
)

func (s AuthStatus) Valid() bool {
	switch s {
	case AuthAuthenticating, AuthNeedsInput, AuthAuthenticated, AuthFailed:
		return true
	default:
		return false
	}
}

// AccountStatus is the account lifecycle state.
type AccountStatus string

const (
	AccountPending     AccountStatus = "PENDING"
	AccountActive      AccountStatus = "ACTIVE"
	AccountPaused      AccountStatus = "PAUSED"
	AccountQuarantined AccountStatus = "QUARANTINED"
	AccountDead        AccountStatus = "DEAD"
	AccountArchived    AccountStatus = "ARCHIVED"
)

func (s AccountStatus) Valid() bool {
	switch s {
	case AccountPending, AccountActive, AccountPaused, AccountQuarantined, AccountDead, AccountArchived:
		return true
	default:
		return false
	}
}

// WorkerStatus is the reported runtime health of a worker container.
type WorkerStatus string

const (
	WorkerPending     WorkerStatus = "PENDING"
	WorkerReady       WorkerStatus = "READY"
	WorkerIdle        WorkerStatus = "IDLE"
	WorkerBusy        WorkerStatus = "BUSY"
	WorkerDraining    WorkerStatus = "DRAINING"
	WorkerError       WorkerStatus = "ERROR"
	WorkerDead        WorkerStatus = "DEAD"
	WorkerQuarantined WorkerStatus = "QUARANTINED"
)

func (s WorkerStatus) Valid() bool {
	switch s {
	case WorkerPending, WorkerReady, WorkerIdle, WorkerBusy, WorkerDraining, WorkerError, WorkerDead, WorkerQuarantined:
		return true
	default:
		return false
	}
}

// DesiredState is the declarative intent the reconciler drives toward.
type DesiredState string

const (
	DesiredRunning DesiredState = "RUNNING"
	DesiredStopped DesiredState = "STOPPED"
)

func (s DesiredState) Valid() bool {
	return s == DesiredRunning || s == DesiredStopped
}

// WorkerSource records how a worker came to exist. It gates auto-deletion:
// only AUTO workers are removed when they hold zero accounts.
type WorkerSource string

const (
	SourceManual WorkerSource = "MANUAL"
	SourceAuto   WorkerSource = "AUTO"
)

// ProvisionOp is the kind of provisioning action recorded in ProvisionLog.
type ProvisionOp string

const (
	OpCreate ProvisionOp = "CREATE"
	OpDelete ProvisionOp = "DELETE"
)

// ProvisionStatus is the outcome of a provisioning op.
type ProvisionStatus string

const (
	ProvisionPending ProvisionStatus = "PENDING"
	ProvisionApplied ProvisionStatus = "APPLIED"
	ProvisionFailed  ProvisionStatus = "FAILED"
)
