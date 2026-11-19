package port

import (
	"context"
	"time"
)

// BrowserSessionStore persists one encrypted browser session per account.
//
// A session is a BEARER CREDENTIAL: whoever holds it can act as the account
// without its password. The store therefore follows the same rule as the
// account's password blob — Get is the single narrow read, used only by the
// control-message dispatch that hands it to a worker, and no list method ever
// returns the ciphertext.
type BrowserSessionStore interface {
	// GetState returns the sealed storageState for an account, or nil when the
	// account has never logged in. A nil return is NOT an error: it is the
	// normal state of a fresh account, and the caller falls back to a login.
	GetState(ctx context.Context, accountID string) ([]byte, error)
	// Save replaces the stored session. A fresh login always wins — keeping an
	// older cookie would let a revoked session shadow a live one. capturedOn is
	// the worker that produced it, and is nil when that container is already
	// gone (a session outlives the container it was captured on).
	Save(ctx context.Context, accountID string, sealed []byte, expiresAt *time.Time, capturedOn *string) error
	// Touch records a successful hydration.
	Touch(ctx context.Context, accountID string) error
	// RefreshState writes back the session a successful action left behind. The
	// platform rotates its tokens during a normal action, so the stored cookie
	// would otherwise be stale while still looking valid. A nil expiresAt keeps
	// whatever date is already stored (re-sealing must not drop a live schedule,
	// or the account falls out of the refresh sweep).
	RefreshState(ctx context.Context, accountID string, sealed []byte, expiresAt *time.Time, workerID *string) error
	// Delete drops a session whose login was rejected, so the next dispatch
	// cannot re-import a cookie the platform has already invalidated.
	Delete(ctx context.Context, accountID string) error
	// DueBefore lists accounts whose dated cookie dies before the cutoff.
	DueBefore(ctx context.Context, cutoff time.Time, limit int) ([]string, error)
}
