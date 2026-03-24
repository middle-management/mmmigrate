package postgres

import _ "github.com/jackc/pgx/v5/stdlib"

// Dialect implements migrate.Dialect for PostgreSQL.
type Dialect struct{}

func (Dialect) DriverName() string { return "pgx" }

func (Dialect) CreateMigrationsTable() string {
	return `
		CREATE SCHEMA IF NOT EXISTS mmmigrate;
		CREATE TABLE IF NOT EXISTS mmmigrate.applied (
			version     INTEGER PRIMARY KEY,
			name        TEXT NOT NULL,
			applied_at  TIMESTAMPTZ NOT NULL DEFAULT now()
		)`
}

func (Dialect) CreateCurrentTable() string {
	return `
		CREATE TABLE IF NOT EXISTS mmmigrate.current (
			id          INTEGER PRIMARY KEY DEFAULT 1,
			checksum    TEXT NOT NULL,
			applied_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
			CONSTRAINT  current_single_row CHECK (id = 1)
		)`
}

func (Dialect) SelectApplied() string {
	return "SELECT version, name, applied_at FROM mmmigrate.applied ORDER BY version"
}

func (Dialect) SelectCurrentChecksum() string {
	return "SELECT checksum FROM mmmigrate.current WHERE id = 1"
}

func (Dialect) InsertApplied() string {
	return "INSERT INTO mmmigrate.applied (version, name, applied_at) VALUES ($1, $2, now())"
}

func (Dialect) UpsertCurrent() string {
	return `
		INSERT INTO mmmigrate.current (id, checksum, applied_at)
		VALUES (1, $1, now())
		ON CONFLICT (id) DO UPDATE SET
			checksum = EXCLUDED.checksum,
			applied_at = EXCLUDED.applied_at`
}

// Lock key: first 8 hex chars of sha256("mmmigrate") = 0x6d4d4d49 = 1833701705
func (Dialect) Lock() string   { return "SELECT pg_advisory_lock(1833701705)" }
func (Dialect) Unlock() string { return "SELECT pg_advisory_unlock(1833701705)" }

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
