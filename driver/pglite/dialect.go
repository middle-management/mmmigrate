// Package pglite provides an mmmigrate Dialect and database/sql driver
// adapter that targets pglite (a WASM build of PostgreSQL) running in a JS
// host (browser or Node). The driver is only usable when compiled with
// GOOS=js GOARCH=wasm.
package pglite

import (
	"io/fs"

	"github.com/middle-management/mmmigrate/selfmigrations"
)

// Dialect implements mmmigrate.Dialect for pglite.
//
// pglite is real PostgreSQL, so the SQL is identical to the postgres driver
// with two differences: the driver name is "pglite" (informational only — the
// driver is wired up via sql.OpenDB / NewConnector, not sql.Open) and Lock /
// Unlock are no-ops because pglite is single-process.
type Dialect struct{}

func (Dialect) DriverName() string { return "pglite" }

func (Dialect) CreateMigrationsTable() string {
	return `
		CREATE SCHEMA IF NOT EXISTS mmmigrate;
		CREATE TABLE IF NOT EXISTS mmmigrate.applied (
			namespace   TEXT NOT NULL,
			version     INTEGER NOT NULL,
			name        TEXT NOT NULL,
			applied_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
			PRIMARY KEY (namespace, version)
		)`
}

func (Dialect) SelfMigrations() fs.FS { return selfmigrations.Postgres }

func (Dialect) SelectApplied() string {
	return "SELECT version, name, applied_at FROM mmmigrate.applied WHERE namespace = $1 ORDER BY version"
}

func (Dialect) SelectCurrentChecksum() string {
	return "SELECT checksum FROM mmmigrate.current WHERE namespace = $1"
}

func (Dialect) InsertApplied() string {
	return "INSERT INTO mmmigrate.applied (namespace, version, name, applied_at) VALUES ($1, $2, $3, now())"
}

func (Dialect) UpsertCurrent() string {
	return `
		INSERT INTO mmmigrate.current (namespace, checksum, applied_at)
		VALUES ($1, $2, now())
		ON CONFLICT (namespace) DO UPDATE SET
			checksum = EXCLUDED.checksum,
			applied_at = EXCLUDED.applied_at`
}

// Lock / Unlock are no-ops: pglite runs in a single wasm instance, so there
// is no possibility of concurrent migration runs.
func (Dialect) Lock() string   { return "" }
func (Dialect) Unlock() string { return "" }

func (Dialect) ResetSQL() string {
	return `
		DROP SCHEMA IF EXISTS mmmigrate CASCADE;
		DO $$ DECLARE
			r RECORD;
		BEGIN
			FOR r IN (SELECT tablename FROM pg_tables WHERE schemaname = 'public') LOOP
				EXECUTE 'DROP TABLE IF EXISTS public.' || quote_ident(r.tablename) || ' CASCADE';
			END LOOP;
		END $$`
}
