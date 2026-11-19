package repository

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/ahdirmai/jg-smm/apps/api/internal/repository/sqlcgen"
)

// TestStatelessPoolSchemaIsInPlace proves 000024's three load-bearing effects on
// a database that has been through every migration: the location triple is
// all-or-nothing, the account carries its own timezone, and the old
// one-account-per-platform placement rule is gone (worker_id is a lease now).
func TestStatelessPoolSchemaIsInPlace(t *testing.T) {
	pool := testPool(t)
	repo := NewAccountRepo(sqlcgen.New(pool))
	ctx := context.Background()
	suffix := p2Suffix(t)

	// Half a location is a row no browser context can be built from.
	var id string
	if err := pool.QueryRow(ctx,
		`INSERT INTO account (platform, username, password_enc, status)
		 VALUES ('INSTAGRAM', $1, decode('00','hex'), 'ACTIVE') RETURNING id::text`,
		"loc-half-"+suffix).Scan(&id); err != nil {
		t.Fatalf("seed account: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM account WHERE id=$1`, id) })

	if _, err := pool.Exec(ctx, `UPDATE account SET location = 'Jakarta' WHERE id = $1`, id); err == nil {
		t.Fatalf("a city without coordinates must violate account_location_coords_pairing")
	}
	if _, err := pool.Exec(ctx,
		`UPDATE account SET location = 'Makassar', latitude = -5.1477, longitude = 119.4327, timezone = 'Asia/Makassar' WHERE id = $1`,
		id); err != nil {
		t.Fatalf("a complete location must be accepted: %v", err)
	}

	byID, err := repo.GetByID(ctx, id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if byID.Location == nil || *byID.Location != "Makassar" ||
		byID.Latitude == nil || *byID.Latitude != -5.1477 ||
		byID.Longitude == nil || *byID.Longitude != 119.4327 {
		t.Fatalf("location not round-tripped: %+v", byID)
	}
	if byID.Timezone != "Asia/Makassar" {
		t.Fatalf("timezone = %q, want Asia/Makassar", byID.Timezone)
	}

	// A row created without stating a zone inherits WIB, never the host's.
	var otherID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO account (platform, username, password_enc, status)
		 VALUES ('INSTAGRAM', $1, decode('00','hex'), 'ACTIVE') RETURNING id::text`,
		"tz-default-"+suffix).Scan(&otherID); err != nil {
		t.Fatalf("seed default-tz account: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM account WHERE id=$1`, otherID) })
	def, err := repo.GetByID(ctx, otherID)
	if err != nil {
		t.Fatalf("get default: %v", err)
	}
	if def.Timezone != "Asia/Jakarta" {
		t.Fatalf("default timezone = %q, want Asia/Jakarta", def.Timezone)
	}

	// The backfill list only returns the accounts still missing a city.
	unlocated, err := repo.ListUnlocated(ctx, 500)
	if err != nil {
		t.Fatalf("list unlocated: %v", err)
	}
	var sawLocated bool
	for _, a := range unlocated {
		if a.ID == id {
			sawLocated = true
		}
	}
	if sawLocated {
		t.Fatalf("a located account was returned by ListUnlocated")
	}

	// The placement unique key must be gone: two Instagram accounts may both be
	// idle (NULL worker) at once, which the old constraint rejected.
	if _, err := pool.Exec(ctx,
		`INSERT INTO account (platform, username, password_enc, status, worker_id)
		 VALUES ('INSTAGRAM', $1, decode('00','hex'), 'ACTIVE', NULL)`,
		"idle-two-"+suffix); err != nil {
		t.Fatalf("a second idle instagram account must be insertable: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM account WHERE username = $1 OR username = $2`,
			"idle-two-"+suffix, "idle-three-"+suffix)
	})
	if _, err := pool.Exec(ctx,
		`INSERT INTO account (platform, username, password_enc, status, worker_id)
		 VALUES ('INSTAGRAM', $1, decode('00','hex'), 'ACTIVE', NULL)`,
		"idle-three-"+suffix); err != nil {
		t.Fatalf("a third idle instagram account must be insertable: %v", err)
	}
}

// TestRefreshStateKeepsStoredExpiry proves the write-back upsert: a rotation
// replaces the cookie, and a capture with no dated cookie must not erase the
// schedule the refresh loop depends on.
func TestRefreshStateKeepsStoredExpiry(t *testing.T) {
	pool := testPool(t)
	sessions := NewSessionRepo(sqlcgen.New(pool))
	ctx := context.Background()
	suffix := p2Suffix(t)

	var id string
	if err := pool.QueryRow(ctx,
		`INSERT INTO account (platform, username, password_enc, status)
		 VALUES ('INSTAGRAM', $1, decode('00','hex'), 'ACTIVE') RETURNING id::text`,
		"state-"+suffix).Scan(&id); err != nil {
		t.Fatalf("seed account: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM account WHERE id=$1`, id) })

	expiry := time.Now().Add(14 * 24 * time.Hour).UTC().Truncate(time.Second)
	if err := sessions.Save(ctx, id, []byte("sealed-v1"), &expiry, nil); err != nil {
		t.Fatalf("save: %v", err)
	}

	// A rotation carried no cookie date: the stored one has to survive.
	if err := sessions.RefreshState(ctx, id, []byte("sealed-v2"), nil, nil); err != nil {
		t.Fatalf("refresh without expiry: %v", err)
	}
	var stored pgtype.Timestamptz
	var state []byte
	if err := pool.QueryRow(ctx,
		`SELECT expires_at, storage_state FROM account_session WHERE account_id = $1`, id).
		Scan(&stored, &state); err != nil {
		t.Fatalf("read session: %v", err)
	}
	if string(state) != "sealed-v2" {
		t.Fatalf("storage_state not replaced by the write-back")
	}
	if !stored.Valid || !stored.Time.Equal(expiry) {
		t.Fatalf("RefreshState erased the stored expiry: %v", stored)
	}

	// A later capture that DOES date the cookie wins.
	later := time.Now().Add(30 * 24 * time.Hour).UTC().Truncate(time.Second)
	if err := sessions.RefreshState(ctx, id, []byte("sealed-v3"), &later, nil); err != nil {
		t.Fatalf("refresh with expiry: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT expires_at FROM account_session WHERE account_id = $1`, id).Scan(&stored); err != nil {
		t.Fatalf("read expiry: %v", err)
	}
	if !stored.Time.Equal(later) {
		t.Fatalf("new expiry not stored: %v want %v", stored.Time, later)
	}
}
