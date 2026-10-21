package mmmigrate

import "io/fs"

// Dialect abstracts database-specific SQL syntax.
//
// Every tracking query is scoped to a namespace, which is always passed as the
// first bind argument. This lets several independent migration histories live
// in the same database.
type Dialect interface {
	DriverName() string
	// CreateMigrationsTable creates the applied table if it doesn't exist. It
	// must never change: every later change to the tracking tables is a
	// migration in SelfMigrations, recorded in this table.
	CreateMigrationsTable() string
	// SelfMigrations returns the migrations for mmmigrate's own tracking
	// tables (see package selfmigrations), rooted at the migration files.
	SelfMigrations() fs.FS
	// SelectApplied takes (namespace) and returns (version, name, applied_at) rows.
	SelectApplied() string
	// SelectCurrentChecksum takes (namespace) and returns a single checksum.
	SelectCurrentChecksum() string
	// InsertApplied takes (namespace, version, name).
	InsertApplied() string
	// UpsertCurrent takes (namespace, checksum).
	UpsertCurrent() string
	// Lock acquires an advisory lock to prevent concurrent migration runs in
	// the same namespace. It takes (namespace).
	// Returns empty string if the database handles concurrency natively (e.g. SQLite).
	Lock() string
	// Unlock releases the advisory lock. It takes (namespace).
	// Returns empty string if Lock is a no-op.
	Unlock() string
	// ResetSQL returns SQL that drops all user objects so the database can be
	// replayed from scratch. Used by shadow database verification.
	ResetSQL() string
}
