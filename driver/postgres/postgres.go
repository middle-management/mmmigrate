package postgres

import (
	"io/fs"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/middle-management/mmmigrate/selfmigrations"
)

// Dialect implements migrate.Dialect for PostgreSQL.
type Dialect struct{}

func (Dialect) DriverName() string { return "pgx" }

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

// Lock uses the two-key advisory lock form: the first key identifies mmmigrate
// (first 8 hex chars of sha256("mmmigrate") = 0x6d4d4d49 = 1833701705) and the
// second is a hash of the namespace, so different namespaces don't block each
// other.
func (Dialect) Lock() string   { return "SELECT pg_advisory_lock(1833701705, hashtext($1))" }
func (Dialect) Unlock() string { return "SELECT pg_advisory_unlock(1833701705, hashtext($1))" }

// ResetSQL drops every non-system schema, with everything in it, and
// recreates an empty public schema. Schemas named pg_* are reserved for the
// system (catalog, TOAST and temp schemas).
func (Dialect) ResetSQL() string {
	return `
		DO $$ DECLARE
			r RECORD;
		BEGIN
			FOR r IN (
				SELECT nspname FROM pg_namespace
				WHERE nspname NOT LIKE 'pg\_%' AND nspname <> 'information_schema'
			) LOOP
				-- IF EXISTS: an earlier CASCADE may already have dropped it, e.g.
				-- a schema owned by an extension in another schema.
				EXECUTE 'DROP SCHEMA IF EXISTS ' || quote_ident(r.nspname) || ' CASCADE';
			END LOOP;
		END $$;
		CREATE SCHEMA public;
		GRANT USAGE ON SCHEMA public TO PUBLIC`
}
