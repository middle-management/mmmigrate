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
