package migrate

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

var defaultDialect Dialect

// RegisterDialect sets the default dialect. Called by driver packages at init.
func RegisterDialect(d Dialect) { defaultDialect = d }

// DefaultDialect returns the dialect registered via driver import.
func DefaultDialect() Dialect { return defaultDialect }
