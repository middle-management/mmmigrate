package postgres_test

import (
	"database/sql"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/middle-management/mmmigrate/driver/postgres"
	"github.com/middle-management/mmmigrate/migrate"
	"github.com/middle-management/mmmigrate/migrate/migratetest"
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
		Dialect: func(t *testing.T) migrate.Dialect {
			return postgres.Dialect{}
		},
		DumpSchema:               dumpPostgresSchema,
		TrackingTable:            "mmmigrate.applied",
		SupportsTransactionalDDL: true,
	})
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
