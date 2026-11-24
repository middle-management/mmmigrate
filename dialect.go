package mmmigrate

import (
	"context"
	"database/sql"
	"io/fs"
)

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

// Resetter is implemented by dialects whose reset can't be written as a single
// SQL script, for example because some objects can't be dropped from dynamic
// SQL. Shadow verification calls Reset in place of executing ResetSQL.
type Resetter interface {
	// Reset drops all user objects through conn so the database can be
	// replayed from scratch.
	Reset(ctx context.Context, conn *sql.Conn) error
}

// Locker is implemented by dialects whose database has no session lock to
// serialise runs, so they take one another way, such as a lease row (Spanner).
// When a dialect implements it, the engine calls AcquireLock in place of
// executing Lock and Unlock, which may then return "".
type Locker interface {
	// AcquireLock blocks until it holds the lock for namespace, or ctx is
	// done. conn is the connection the run uses; db is its pool, for work the
	// lock does while the run's statements hold conn, such as renewing a
	// lease. It returns a context derived from ctx that is cancelled if the
	// lock is lost before it is released, which the engine runs the work under
	// the lock with, and a function that releases the lock.
	AcquireLock(ctx context.Context, db *sql.DB, conn *sql.Conn, namespace string) (context.Context, func(), error)
}

// Executor is implemented by dialects whose database can't run a migration
// and its record in one transaction, for example because DDL can't run in a
// transaction at all (Spanner), or can't run a script of several statements
// in one call. When a dialect implements it, the engine hands it every script
// it would otherwise execute itself: CreateMigrationsTable, migrations,
// current.sql and shadow baselines.
type Executor interface {
	// Exec runs script, one or more statements, outside a transaction.
	Exec(ctx context.Context, conn *sql.Conn, script string) error
	// ApplyMigration runs a numbered migration's SQL and then records it as
	// applied in namespace (InsertApplied), only once all of it has
	// succeeded. A migration that fails, or whose run is interrupted, must
	// not be recorded, and the next run must be able to apply it again.
	ApplyMigration(ctx context.Context, conn *sql.Conn, namespace string, version int, name, script string) error
	// ApplyCurrent runs current.sql's (processed) SQL and then records its
	// checksum for namespace (UpsertCurrent).
	ApplyCurrent(ctx context.Context, conn *sql.Conn, namespace, script, checksum string) error
}
