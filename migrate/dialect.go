package migrate

import "sync"

// Dialect abstracts database-specific SQL syntax.
type Dialect interface {
	DriverName() string
	CreateMigrationsTable() string
	CreateCurrentTable() string
	SelectApplied() string
	SelectCurrentChecksum() string
	InsertApplied() string
	UpsertCurrent() string
	// Lock acquires an advisory lock to prevent concurrent migration runs.
	// Returns empty string if the database handles concurrency natively (e.g. SQLite).
	Lock() string
	// Unlock releases the advisory lock. Returns empty string if Lock is a no-op.
	Unlock() string
	// ResetSQL returns SQL that drops all user objects so the database can be
	// replayed from scratch. Used by shadow database verification.
	ResetSQL() string
}

var (
	dialectMu      sync.RWMutex
	defaultDialect Dialect
)

// RegisterDialect sets the default dialect. Called by driver packages at init.
func RegisterDialect(d Dialect) {
	dialectMu.Lock()
	defer dialectMu.Unlock()
	defaultDialect = d
}

// DefaultDialect returns the dialect registered via driver import.
func DefaultDialect() Dialect {
	dialectMu.RLock()
	defer dialectMu.RUnlock()
	return defaultDialect
}
