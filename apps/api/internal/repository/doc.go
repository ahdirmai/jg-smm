// Package repository implements the persistence ports using sqlc + pgx.
//
// Generated queries live in the sqlcgen subpackage and are never edited by hand
// (see sqlc.yaml + db/queries). This package maps generated rows to domain/port
// types so the rest of the app never imports sqlcgen directly.
package repository
