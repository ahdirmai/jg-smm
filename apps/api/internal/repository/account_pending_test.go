package repository

import (
	"context"
	"testing"
	"time"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm/apps/api/internal/repository/sqlcgen"
)

// A never-attempted account must be visible to the refresh scheduler.
//
// The column used to default to AUTHENTICATING, so every import and seed was
// born in a state the due query excludes (it reads AUTHENTICATING as "a login
// is in flight") while carrying last_checked_at = NULL, which the stale-login
// sweeper requires. The row was simultaneously skipped by the scheduler, the
// safety net, and counted as live work by the in-flight gate: 443 imported
// accounts sat invisible for hours and reported a login ceiling of 443.
func TestPendingAccountIsDueForFirstLogin(t *testing.T) {
	pool := testPool(t)
	repo := NewAccountRepo(sqlcgen.New(pool))
	ctx := context.Background()

	username := "pending-" + p2Suffix(t)
	var id string
	// No auth_status and no worker_id: the defaults the importer inherits.
	if err := pool.QueryRow(ctx,
		`INSERT INTO account (platform, username, password_enc, status)
		 VALUES ('INSTAGRAM', $1, decode('00', 'hex'), 'ACTIVE')
		 RETURNING id::text`, username).Scan(&id); err != nil {
		t.Fatalf("seed account: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE account SET worker_id = (SELECT id FROM worker LIMIT 1) WHERE id = $1`, id); err != nil {
		t.Skipf("no worker row to bind the account to: %v", err)
	}

	var status string
	if err := pool.QueryRow(ctx, `SELECT auth_status::text FROM account WHERE id = $1`, id).Scan(&status); err != nil {
		t.Fatalf("read default status: %v", err)
	}
	if status != string(domain.AuthPending) {
		t.Fatalf("a fresh account is %q, want %q: AUTHENTICATING means a login is in flight", status, domain.AuthPending)
	}

	due, err := repo.ListDueForRefresh(ctx, time.Now().Add(168*time.Hour), time.Now().Add(-6*time.Hour))
	if err != nil {
		t.Fatalf("list due: %v", err)
	}
	for _, acc := range due {
		if acc.ID == id {
			return
		}
	}
	t.Fatalf("account %s (PENDING) is not due for its first login", username)
}
