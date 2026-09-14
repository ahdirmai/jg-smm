package repository

import (
	"errors"
	"fmt"
	"net/netip"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

// uuidString converts a pgtype.UUID to its canonical string form. Invalid
// (NULL) UUIDs become the empty string — repos only call this on NOT NULL ids.
func uuidString(id pgtype.UUID) string {
	if !id.Valid {
		return ""
	}
	u, err := uuid.FromBytes(id.Bytes[:])
	if err != nil {
		return fmt.Sprintf("%x", id.Bytes)
	}
	return u.String()
}

// uuidValue converts a string id to pgtype.UUID; empty string becomes NULL.
func uuidValue(s string) pgtype.UUID {
	if s == "" {
		return pgtype.UUID{}
	}
	u, err := uuid.Parse(s)
	if err != nil {
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: u, Valid: true}
}

// textOrNull maps an empty string to SQL NULL.
func textOrNull(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// inetOrNull parses an IP literal, returning NULL when empty or invalid. The
// caller treats a bad IP as "unknown" rather than failing the request.
func inetOrNull(s string) *netip.Addr {
	if s == "" {
		return nil
	}
	addr, err := netip.ParseAddr(s)
	if err != nil {
		return nil
	}
	return &addr
}

// isUniqueViolation reports whether err is a Postgres unique-constraint error
// (SQLSTATE 23505).
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
