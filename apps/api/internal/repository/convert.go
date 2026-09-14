package repository

import (
	"fmt"

	"github.com/google/uuid"
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
