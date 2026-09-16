package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm/apps/api/internal/port"
	"github.com/ahdirmai/jg-smm/apps/api/internal/repository/sqlcgen"
)

// AccountRepo persists worker (executor) accounts. password_enc is never
// selected by any query here (write-only credential, P1-07): every query below
// names its columns explicitly and omits it.
type AccountRepo struct {
	q *sqlcgen.Queries
}

// NewAccountRepo binds the repo to a sqlc query handle.
func NewAccountRepo(q *sqlcgen.Queries) *AccountRepo { return &AccountRepo{q: q} }

var _ port.AccountStore = (*AccountRepo)(nil)

// GetByID returns the account with the given id.
func (r *AccountRepo) GetByID(ctx context.Context, id string) (domain.Account, error) {
	row, err := r.q.GetAccountByID(ctx, uuidValue(id))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Account{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Account{}, fmt.Errorf("repository.account.GetByID: %w", err)
	}
	return toAccount(accountFields(row)), nil
}

// List returns accounts matching the filter. Optional dimensions are applied in
// Go (see WorkerRepo.List); pagination stays in SQL.
func (r *AccountRepo) List(ctx context.Context, f port.AccountFilter) ([]domain.Account, error) {
	limit, offset := pageBounds(f.Limit, f.Offset)
	rows, err := r.q.ListAccounts(ctx, sqlcgen.ListAccountsParams{Limit: limit, Offset: offset})
	if err != nil {
		return nil, fmt.Errorf("repository.account.List: %w", err)
	}
	out := make([]domain.Account, 0, len(rows))
	for _, row := range rows {
		a := toAccount(accountFields(row))
		if f.Platform != nil && a.Platform != *f.Platform {
			continue
		}
		if f.Status != nil && a.Status != *f.Status {
			continue
		}
		if f.WorkerID != nil && (a.WorkerID == nil || *a.WorkerID != *f.WorkerID) {
			continue
		}
		if f.UnassignedOnly && a.WorkerID != nil {
			continue
		}
		out = append(out, a)
	}
	return out, nil
}

// Create inserts an account. Duplicate (platform, username) -> ErrConflict.
// An unset AuthStatus defaults to AUTHENTICATING, matching the DB default and
// the login state machine's entry point (a fresh account is logging in).
func (r *AccountRepo) Create(ctx context.Context, a domain.Account) (domain.Account, error) {
	authStatus := a.AuthStatus
	if authStatus == "" {
		authStatus = domain.AuthAuthenticating
	}
	row, err := r.q.CreateAccount(ctx, sqlcgen.CreateAccountParams{
		ID:           uuidValue(a.ID),
		Platform:     platformEnum(a.Platform),
		Username:     a.Username,
		PasswordEnc:  a.PasswordEnc,
		AuthStatus:   sqlcgen.AuthStatus(authStatus),
		ProxyGroupID: uuidValuePtr(a.ProxyGroupID),
		Status:       sqlcgen.AccountStatus(a.Status),
		Tags:         nonNilTags(a.Tags),
		WorkerID:     uuidValuePtr(a.WorkerID),
	})
	if err != nil {
		if isUniqueViolation(err) {
			return domain.Account{}, domain.ErrConflict
		}
		return domain.Account{}, fmt.Errorf("repository.account.Create: %w", err)
	}
	return toAccount(accountFields(row)), nil
}

// Update persists mutable lifecycle/auth fields. It never touches password_enc.
func (r *AccountRepo) Update(ctx context.Context, a domain.Account) (domain.Account, error) {
	row, err := r.q.UpdateAccount(ctx, sqlcgen.UpdateAccountParams{
		ID:             uuidValue(a.ID),
		AuthStatus:     sqlcgen.AuthStatus(a.AuthStatus),
		Handle:         a.Handle,
		LastVerifiedAt: tsPtr(a.LastVerified),
		ProxyGroupID:   uuidValuePtr(a.ProxyGroupID),
		HealthScore:    int32(a.HealthScore),
		Status:         sqlcgen.AccountStatus(a.Status),
		Tags:           a.Tags,
		WorkerID:       uuidValuePtr(a.WorkerID),
		LastUsedAt:     tsPtr(a.LastUsedAt),
		LastCheckedAt:  tsPtr(a.LastChecked),
		LastError:      a.LastError,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Account{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Account{}, fmt.Errorf("repository.account.Update: %w", err)
	}
	return toAccount(accountFields(row)), nil
}

// Assign packs an account into a worker. UNIQUE(worker_id, platform) violations
// (a same-platform account already hosted there) map to domain.ErrConflict.
func (r *AccountRepo) Assign(ctx context.Context, accountID, workerID string) (domain.Account, error) {
	row, err := r.q.AssignAccount(ctx, sqlcgen.AssignAccountParams{
		ID:       uuidValue(accountID),
		WorkerID: uuidValue(workerID),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Account{}, domain.ErrNotFound
	}
	if err != nil {
		if isUniqueViolation(err) {
			return domain.Account{}, domain.ErrConflict
		}
		return domain.Account{}, fmt.Errorf("repository.account.Assign: %w", err)
	}
	return toAccount(accountFields(row)), nil
}

// Unassign detaches an account from its worker.
func (r *AccountRepo) Unassign(ctx context.Context, accountID string) (domain.Account, error) {
	row, err := r.q.UnassignAccount(ctx, uuidValue(accountID))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Account{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Account{}, fmt.Errorf("repository.account.Unassign: %w", err)
	}
	return toAccount(accountFields(row)), nil
}

// Delete removes an account.
func (r *AccountRepo) Delete(ctx context.Context, id string) error {
	if err := r.q.DeleteAccount(ctx, uuidValue(id)); err != nil {
		return fmt.Errorf("repository.account.Delete: %w", err)
	}
	return nil
}

// ListByWorker returns the accounts hosted by a container.
func (r *AccountRepo) ListByWorker(ctx context.Context, workerID string) ([]domain.Account, error) {
	rows, err := r.q.ListAccountsByWorker(ctx, uuidValue(workerID))
	if err != nil {
		return nil, fmt.Errorf("repository.account.ListByWorker: %w", err)
	}
	out := make([]domain.Account, 0, len(rows))
	for _, row := range rows {
		out = append(out, toAccount(accountFields(row)))
	}
	return out, nil
}

// CountByWorker returns how many accounts a container currently hosts.
func (r *AccountRepo) CountByWorker(ctx context.Context, workerID string) (int, error) {
	n, err := r.q.CountAccountsByWorker(ctx, uuidValue(workerID))
	if err != nil {
		return 0, fmt.Errorf("repository.account.CountByWorker: %w", err)
	}
	return int(n), nil
}

// nonNilTags guarantees a non-nil slice: a nil []string would be sent as SQL
// NULL and rejected by the NOT NULL tags column (whose default is '{}').
func nonNilTags(tags []string) []string {
	if tags == nil {
		return []string{}
	}
	return tags
}

// accountFields is the shared column set every account query selects. Every
// generated account row type has exactly these fields in this order, so it is
// convertible with a plain type conversion (see call sites).
type accountFields struct {
	ID             pgtype.UUID
	Platform       sqlcgen.Platform
	Username       string
	AuthStatus     sqlcgen.AuthStatus
	Handle         *string
	LastVerifiedAt pgtype.Timestamptz
	ProxyGroupID   pgtype.UUID
	HealthScore    int32
	Status         sqlcgen.AccountStatus
	Tags           []string
	WorkerID       pgtype.UUID
	LastUsedAt     pgtype.Timestamptz
	LastCheckedAt  pgtype.Timestamptz
	LastError      *string
	CreatedAt      pgtype.Timestamptz
}

// toAccount maps an account row to the domain entity. It never reads a
// password column: no account query selects one.
func toAccount(f accountFields) domain.Account {
	return domain.Account{
		ID:           uuidString(f.ID),
		Platform:     platformDomain(f.Platform),
		Username:     f.Username,
		AuthStatus:   domain.AuthStatus(f.AuthStatus),
		Handle:       f.Handle,
		LastVerified: tsTime(f.LastVerifiedAt),
		ProxyGroupID: uuidStrPtr(f.ProxyGroupID),
		HealthScore:  int(f.HealthScore),
		Status:       domain.AccountStatus(f.Status),
		Tags:         f.Tags,
		WorkerID:     uuidStrPtr(f.WorkerID),
		LastUsedAt:   tsTime(f.LastUsedAt),
		LastChecked:  tsTime(f.LastCheckedAt),
		LastError:    f.LastError,
		CreatedAt:    tsTimeOrZero(f.CreatedAt),
	}
}
