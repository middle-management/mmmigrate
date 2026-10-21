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
		db.Exec("DROP SCHEMA IF EXISTS mmmigrate, app, ext_a, ext_b CASCADE")
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
		// An extension that owns a second schema: dropping ext_a cascades to
		// ext_b, which the reset must then skip rather than fail on.
		"CREATE SCHEMA ext_a",
		"CREATE SCHEMA ext_b",
		"CREATE EXTENSION citext SCHEMA ext_a",
		"ALTER EXTENSION citext ADD SCHEMA ext_b",
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
	err = mmmigrate.VerifyAgainstShadow(ctx, db, postgres.Dialect{}, os.DirFS(dir))
	if err == nil {
		t.Error("shadow verification passed a migration that fails on a fresh database")
	} else if !strings.Contains(err.Error(), "shadow replay failed") {
		t.Errorf("expected the replay to fail, not the reset: %v", err)
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

// TestPostgresShadowBaselineSessionState checks that session settings made by
// a baseline don't reach the replay. pg_dump output empties search_path for
// its session; if that leaked, unqualified names in the migrations would stop
// resolving.
func TestPostgresShadowBaselineSessionState(t *testing.T) {
	dsn := os.Getenv("MMMIGRATE_TEST_POSTGRES_URL")
	if dsn == "" {
		t.Skip("set MMMIGRATE_TEST_POSTGRES_URL to run postgres integration tests")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Exec("DROP SCHEMA IF EXISTS mmmigrate, studio CASCADE")
		db.Exec("DROP TABLE IF EXISTS public.accounts_cache")
		db.Close()
	})

	// Shaped like pg_dump --schema-only output for another app's schema.
	baseline := `
		SELECT pg_catalog.set_config('search_path', '', false);
		CREATE SCHEMA studio;
		CREATE TABLE studio."user" (id int PRIMARY KEY);`

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "001_accounts_cache.sql"),
		[]byte(`CREATE TABLE accounts_cache (user_id int REFERENCES studio."user" (id));`), 0644); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	err = mmmigrate.VerifyAgainstShadow(ctx, db, postgres.Dialect{}, os.DirFS(dir),
		mmmigrate.WithShadowBaseline(baseline))
	if err != nil {
		t.Fatalf("shadow verification with baseline failed: %v", err)
	}
}

// TestPostgresShadowBaselineFromPgDump loads real `pg_dump --schema-only`
// output as a baseline. Plain dumps carry psql-only \restrict/\unrestrict
// lines and reset search_path, and both must not break the replay. The
// fixture comes from pg_dump 14, the oldest supported version, whose output
// newer servers also accept.
func TestPostgresShadowBaselineFromPgDump(t *testing.T) {
	dsn := os.Getenv("MMMIGRATE_TEST_POSTGRES_URL")
	if dsn == "" {
		t.Skip("set MMMIGRATE_TEST_POSTGRES_URL to run postgres integration tests")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	cleanup := func() {
		db.Exec("DROP SCHEMA IF EXISTS mmmigrate, studio CASCADE")
		db.Exec("DROP TABLE IF EXISTS public.membership_cache")
	}
	cleanup()
	t.Cleanup(func() { cleanup(); db.Close() })

	dump, err := os.ReadFile(filepath.Join("testdata", "studio_pg_dump.sql"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "001_membership_cache.sql"),
		[]byte(`CREATE TABLE membership_cache (user_id uuid REFERENCES studio."user" (id));`), 0644); err != nil {
		t.Fatal(err)
	}

	err = mmmigrate.VerifyAgainstShadow(context.Background(), db, postgres.Dialect{}, os.DirFS(dir),
		mmmigrate.WithShadowBaseline(string(dump)))
	if err != nil {
		t.Fatal(err)
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
