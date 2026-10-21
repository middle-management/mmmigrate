package sqlite

import (
	"io/fs"

	"github.com/middle-management/mmmigrate/selfmigrations"
	_ "modernc.org/sqlite"
)

// Dialect implements migrate.Dialect for SQLite.
type Dialect struct{}

func (Dialect) DriverName() string { return "sqlite" }

func (Dialect) CreateMigrationsTable() string {
	return `
		CREATE TABLE IF NOT EXISTS mmmigrate_applied (
			namespace   TEXT NOT NULL,
			version     INTEGER NOT NULL,
			name        TEXT NOT NULL,
			applied_at  TEXT NOT NULL DEFAULT (datetime('now')),
			PRIMARY KEY (namespace, version)
		)`
}

func (Dialect) SelfMigrations() fs.FS { return selfmigrations.SQLite }

func (Dialect) SelectApplied() string {
	return "SELECT version, name, applied_at FROM mmmigrate_applied WHERE namespace = ? ORDER BY version"
}

func (Dialect) SelectCurrentChecksum() string {
	return "SELECT checksum FROM mmmigrate_current WHERE namespace = ?"
}

func (Dialect) InsertApplied() string {
	return "INSERT INTO mmmigrate_applied (namespace, version, name, applied_at) VALUES (?, ?, ?, datetime('now'))"
}

func (Dialect) UpsertCurrent() string {
	return `
		INSERT INTO mmmigrate_current (namespace, checksum, applied_at)
		VALUES (?, ?, datetime('now'))
		ON CONFLICT(namespace) DO UPDATE SET
			checksum = excluded.checksum,
			applied_at = excluded.applied_at`
}

// SQLite uses file-level locking natively; no advisory lock needed.
func (Dialect) Lock() string   { return "" }
func (Dialect) Unlock() string { return "" }

func (Dialect) ResetSQL() string {
	return `
		PRAGMA writable_schema = 1;
		DELETE FROM sqlite_master WHERE type IN ('table', 'view', 'index', 'trigger');
		PRAGMA writable_schema = 0;
		VACUUM`
}
