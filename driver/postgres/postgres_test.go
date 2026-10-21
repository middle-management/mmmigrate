package postgres_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/middle-management/mmmigrate"
	"github.com/middle-management/mmmigrate/driver/postgres"
	"github.com/middle-management/mmmigrate/migratetest"
)

func TestPostgres(t *testing.T) {
	dsn := os.Getenv("MMMIGRATE_TEST_POSTGRES_URL")
	if dsn == "" {
		t.Skip("set MMMIGRATE_TEST_POSTGRES_URL to run postgres integration tests")
	}

	migratetest.Run(t, migratetest.Harness{
		OpenDB: func(t *testing.T) *sql.DB {
			t.Helper()
			db, err := sql.Open("pgx", dsn)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				db.Exec("DROP SCHEMA IF EXISTS mmmigrate CASCADE")
				db.Exec("DROP TABLE IF EXISTS users, posts CASCADE")
				db.Close()
			})
			db.Exec("DROP SCHEMA IF EXISTS mmmigrate CASCADE")
			db.Exec("DROP TABLE IF EXISTS users, posts CASCADE")
			return db
		},
		Dialect: func(t *testing.T) mmmigrate.Dialect {
			return postgres.Dialect{}
		},
		DumpSchema:               dumpPostgresSchema,
		TrackingTable:            "mmmigrate.applied",
		SupportsTransactionalDDL: true,
	})
}

// TestPostgresShadowReset checks that shadow verification starts from an
// empty database: objects left by an earlier replay, in any schema and of any
// kind, must not let a migration pass that would fail on a fresh database.
func TestPostgresShadowReset(t *testing.T) {
	dsn := os.Getenv("MMMIGRATE_TEST_POSTGRES_URL")
	if dsn == "" {
		t.Skip("set MMMIGRATE_TEST_POSTGRES_URL to run postgres integration tests")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	cleanup := func() {
		db.Exec("DROP SCHEMA IF EXISTS mmmigrate, app CASCADE")
		db.Exec("DROP VIEW IF EXISTS public.leftover_view")
		db.Exec("DROP FUNCTION IF EXISTS public.leftover_func()")
		db.Exec("DROP TYPE IF EXISTS public.leftover_type")
		db.Exec("DROP SEQUENCE IF EXISTS public.leftover_seq")
	}
	cleanup()
	t.Cleanup(func() { cleanup(); db.Close() })

	// Leftovers from an earlier replay.
	for _, q := range []string{
		"CREATE SCHEMA app",
		"CREATE TABLE app.z (id int)",
		"CREATE VIEW public.leftover_view AS SELECT 1 AS one",
		"CREATE FUNCTION public.leftover_func() RETURNS int LANGUAGE sql AS 'SELECT 1'",
		"CREATE TYPE public.leftover_type AS ENUM ('a')",
		"CREATE SEQUENCE public.leftover_seq",
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}

	// A migration that depends on app.z without creating it.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "001_add_v.sql"),
		[]byte("CREATE SCHEMA IF NOT EXISTS app; CREATE VIEW app.v AS SELECT id FROM app.z;"), 0644); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	if err := mmmigrate.VerifyAgainstShadow(ctx, db, postgres.Dialect{}, os.DirFS(dir)); err == nil {
		t.Error("shadow verification passed a migration that fails on a fresh database")
	}

	var leftovers []string
	rows, err := db.Query(`
		SELECT 'schema ' || nspname FROM pg_namespace
		WHERE nspname NOT IN ('pg_catalog', 'information_schema', 'public', 'mmmigrate')
		  AND nspname NOT LIKE 'pg\_toast%' AND nspname NOT LIKE 'pg\_temp\_%'
		UNION ALL
		SELECT 'relation ' || relname FROM pg_class WHERE relnamespace = 'public'::regnamespace
		UNION ALL
		SELECT 'function ' || proname FROM pg_proc WHERE pronamespace = 'public'::regnamespace
		UNION ALL
		SELECT 'type ' || typname FROM pg_type WHERE typnamespace = 'public'::regnamespace`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		leftovers = append(leftovers, s)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(leftovers) > 0 {
		t.Errorf("shadow reset left objects behind: %v", leftovers)
	}
}

func dumpPostgresSchema(t *testing.T, db *sql.DB) string {
	t.Helper()

	var version string
	db.QueryRow("SELECT version()").Scan(&version)

	rows, err := db.Query(`
		SELECT table_schema, table_name, column_name, data_type, is_nullable, column_default
		FROM information_schema.columns
		WHERE table_schema NOT IN ('pg_catalog', 'information_schema')
		ORDER BY table_schema, table_name, ordinal_position
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	tables := map[string][]string{}
	for rows.Next() {
		var schema, table, col, dtype, nullable string
		var def sql.NullString
		if err := rows.Scan(&schema, &table, &col, &dtype, &nullable, &def); err != nil {
			t.Fatal(err)
		}
		key := fmt.Sprintf("%s.%s", schema, table)
		line := fmt.Sprintf("  %s %s", col, dtype)
		if nullable == "NO" {
			line += " NOT NULL"
		}
		if def.Valid {
			line += " DEFAULT " + def.String
		}
		tables[key] = append(tables[key], line)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}

	var keys []string
	for k := range tables {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var out strings.Builder
	fmt.Fprintf(&out, "-- server: %s\n\n", version)
	for _, k := range keys {
		fmt.Fprintf(&out, "-- table: %s\n", k)
		for _, col := range tables[k] {
			fmt.Fprintln(&out, col)
		}
		fmt.Fprintln(&out)
	}

	return out.String()
}
