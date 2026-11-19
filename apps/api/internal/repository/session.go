package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/ahdirmai/jg-smm/apps/api/internal/port"
	"github.com/ahdirmai/jg-smm/apps/api/internal/repository/sqlcgen"
)

// SessionRepo persists encrypted per-account browser sessions.
//
// Like AccountRepo's password_enc, storage_state is never selected by a listing
// query: GetState is the one read that returns ciphertext, and every other
// method here is metadata.
type SessionRepo struct {
	q *sqlcgen.Queries
}

// NewSessionRepo binds the repo to a sqlc query handle.
func NewSessionRepo(q *sqlcgen.Queries) *SessionRepo { return &SessionRepo{q: q} }

var _ port.BrowserSessionStore = (*SessionRepo)(nil)

// GetState returns the sealed storageState, or nil when the account has none.
// A missing row is a normal state, not an error: it means the account has never
// logged in on a container that could export one.
func (r *SessionRepo) GetState(ctx context.Context, accountID string) ([]byte, error) {
	row, err := r.q.GetAccountSessionState(ctx, uuidValue(accountID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("repository.session.GetState: %w", err)
	}
	return row.StorageState, nil
}

// Save writes or replaces the session for an account.
func (r *SessionRepo) Save(ctx context.Context, accountID string, sealed []byte, expiresAt *time.Time, capturedOn *string) error {
	var exp pgtype.Timestamptz
	if expiresAt != nil {
		exp = pgtype.Timestamptz{Time: *expiresAt, Valid: true}
	}
	_, err := r.q.UpsertAccountSession(ctx, sqlcgen.UpsertAccountSessionParams{
		AccountID:    uuidValue(accountID),
		StorageState: sealed,
		ExpiresAt:    exp,
		CapturedOn:   uuidValuePtr(capturedOn),
	})
	if err != nil {
		return fmt.Errorf("repository.session.Save: %w", err)
	}
	return nil
}

// Touch records a successful hydration from this session.
func (r *SessionRepo) Touch(ctx context.Context, accountID string) error {
	if err := r.q.TouchAccountSession(ctx, uuidValue(accountID)); err != nil {
		return fmt.Errorf("repository.session.Touch: %w", err)
	}
	return nil
}

// Delete drops the session for an account.
func (r *SessionRepo) Delete(ctx context.Context, accountID string) error {
	if err := r.q.DeleteAccountSession(ctx, uuidValue(accountID)); err != nil {
		return fmt.Errorf("repository.session.Delete: %w", err)
	}
	return nil
}

// DueBefore lists accounts whose dated cookie expires before the cutoff.
func (r *SessionRepo) DueBefore(ctx context.Context, cutoff time.Time, limit int) ([]string, error) {
	rows, err := r.q.ListSessionsExpiringBefore(ctx, sqlcgen.ListSessionsExpiringBeforeParams{
		ExpiresAt: pgtype.Timestamptz{Time: cutoff, Valid: true},
		Limit:     int32(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("repository.session.DueBefore: %w", err)
	}
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		out = append(out, uuidString(row.AccountID))
	}
	return out, nil
}

// RefreshState writes back the session a successful action left behind. Unlike
// Save it is not a "fresh login wins" replace: the cookie was rotated by the
// platform mid-action, so it is strictly newer, and a nil expiry defers to the
// stored one (the query's COALESCE) so a re-seal cannot erase a refresh
// schedule the fleet still relies on.
func (r *SessionRepo) RefreshState(ctx context.Context, accountID string, sealed []byte, expiresAt *time.Time, workerID *string) error {
	var exp pgtype.Timestamptz
	if expiresAt != nil {
		exp = pgtype.Timestamptz{Time: *expiresAt, Valid: true}
	}
	if err := r.q.RefreshAccountSessionState(ctx, sqlcgen.RefreshAccountSessionStateParams{
		AccountID:    uuidValue(accountID),
		StorageState: sealed,
		ExpiresAt:    exp,
		CapturedOn:   uuidValuePtr(workerID),
	}); err != nil {
		return fmt.Errorf("repository.session.RefreshState: %w", err)
	}
	return nil
}
