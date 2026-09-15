package repository

import (
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/repository/sqlcgen"
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

// pageBounds clamps limit/offset to sane bounds for list queries.
func pageBounds(limit, offset int) (int32, int32) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	return int32(limit), int32(offset)
}

// platformEnum maps a lowercase domain platform to its UPPER Postgres enum.
func platformEnum(p domain.Platform) sqlcgen.Platform {
	return sqlcgen.Platform(strings.ToUpper(string(p)))
}

// platformDomain maps a Postgres enum back to lowercase domain platform.
func platformDomain(p sqlcgen.Platform) domain.Platform {
	return domain.Platform(strings.ToLower(string(p)))
}

// intPtr / int32Ptr round-trip nullable ints.
func intPtr(p *int32) *int {
	if p == nil {
		return nil
	}
	return ptr(int(*p))
}

func int32Ptr(p *int) *int32 {
	if p == nil {
		return nil
	}
	return ptr(int32(*p))
}

func ptr[T any](v T) *T { return &v }

// tsTime converts a pgtype timestamp to *time.Time (nil when invalid).
func tsTime(ts pgtype.Timestamptz) *time.Time {
	if !ts.Valid {
		return nil
	}
	t := ts.Time.UTC()
	return &t
}

// tsTimeOrZero is tsTime for NOT NULL columns: an invalid value falls back
// to the zero time rather than nil (the DB default of now() covers real rows).
func tsTimeOrZero(ts pgtype.Timestamptz) time.Time {
	if !ts.Valid {
		return time.Time{}
	}
	return ts.Time.UTC()
}

// tsPtr converts *time.Time to a pgtype timestamp (invalid when nil/zero).
func tsPtr(t *time.Time) pgtype.Timestamptz {
	if t == nil || t.IsZero() {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: t.UTC(), Valid: true}
}

// uuidStrPtr converts a nullable pgtype UUID to *string.
func uuidStrPtr(id pgtype.UUID) *string {
	if !id.Valid {
		return nil
	}
	return ptr(uuidString(id))
}

// uuidValuePtr converts *string to a nullable pgtype UUID.
func uuidValuePtr(id *string) pgtype.UUID {
	if id == nil {
		return pgtype.UUID{}
	}
	return uuidValue(*id)
}
