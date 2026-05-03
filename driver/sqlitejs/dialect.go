// Package sqlitejs provides an mmmigrate Dialect for SQLite running inside
// a JS host (e.g. @sqlite.org/sqlite-wasm wrapped in the jsdb adapter
// shape). The SQL is identical to driver/sqlite but this package has no
// modernc.org/sqlite dependency, so it builds for any target.
package sqlitejs

// Dialect implements mmmigrate.Dialect for SQLite via the jsdb bridge.
type Dialect struct{}

func (Dialect) DriverName() string { return "sqlitejs" }

func (Dialect) CreateMigrationsTable() string {
	return `
		CREATE TABLE IF NOT EXISTS mmmigrate_applied (
			version     INTEGER PRIMARY KEY,
			name        TEXT NOT NULL,
			applied_at  TEXT NOT NULL DEFAULT (datetime('now'))
		)`
}

func (Dialect) CreateCurrentTable() string {
	return `
		CREATE TABLE IF NOT EXISTS mmmigrate_current (
			id          INTEGER PRIMARY KEY DEFAULT 1 CHECK (id = 1),
			checksum    TEXT NOT NULL,
			applied_at  TEXT NOT NULL DEFAULT (datetime('now'))
		)`
}

func (Dialect) SelectApplied() string {
	return "SELECT version, name, applied_at FROM mmmigrate_applied ORDER BY version"
}

func (Dialect) SelectCurrentChecksum() string {
	return "SELECT checksum FROM mmmigrate_current WHERE id = 1"
}

func (Dialect) InsertApplied() string {
	return "INSERT INTO mmmigrate_applied (version, name, applied_at) VALUES (?, ?, datetime('now'))"
}

func (Dialect) UpsertCurrent() string {
	return `
		INSERT INTO mmmigrate_current (id, checksum, applied_at)
		VALUES (1, ?, datetime('now'))
		ON CONFLICT(id) DO UPDATE SET
			checksum = excluded.checksum,
			applied_at = excluded.applied_at`
}

// SQLite uses file-level locking natively (and the WASM build is single-process).
func (Dialect) Lock() string   { return "" }
func (Dialect) Unlock() string { return "" }

func (Dialect) ResetSQL() string {
	return `
		PRAGMA writable_schema = 1;
		DELETE FROM sqlite_master WHERE type IN ('table', 'view', 'index', 'trigger');
		PRAGMA writable_schema = 0;
		VACUUM`
}
