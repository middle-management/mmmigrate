// Package gosqlspannertest runs the spanner dialect's integration tests over
// github.com/googleapis/go-sql-spanner, Spanner's own database/sql driver,
// instead of pgx through PGAdapter: the dialect must not depend on PGAdapter.
// It is a module of its own so that the dialect's module doesn't require the
// Spanner client libraries.
//
// Against the emulator (its gRPC port, 9010 in the pgadapter-emulator image):
//
//	MMMIGRATE_TEST_SPANNER_EMULATOR_HOST=localhost:9010 go test ./...
package gosqlspannertest

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"

	_ "github.com/googleapis/go-sql-spanner"

	"github.com/middle-management/mmmigrate"
	"github.com/middle-management/mmmigrate/driver/spanner"
	"github.com/middle-management/mmmigrate/migratetest"
)

const envHost = "MMMIGRATE_TEST_SPANNER_EMULATOR_HOST"

func openDB(t *testing.T) *sql.DB {
	t.Helper()
	host := os.Getenv(envHost)
	if host == "" {
		t.Skip("set " + envHost + " to run the go-sql-spanner integration tests")
	}
	// autoConfigEmulator creates the instance and the database, of the
	// PostgreSQL dialect, on first use.
	dsn := host + "/projects/mmmigrate/instances/test/databases/gosql;autoConfigEmulator=true;dialect=POSTGRESQL"
	db, err := sql.Open("spanner", dsn)
	if err != nil {
		t.Fatal(err)
	}
	reset := func() {
		conn, err := db.Conn(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		if err := (spanner.Dialect{}).Reset(context.Background(), conn); err != nil {
			t.Fatalf("reset: %v", err)
		}
	}
	reset()
	t.Cleanup(func() {
		reset()
		db.Close()
	})
	return db
}

func TestGoSQLSpanner(t *testing.T) {
	if os.Getenv(envHost) == "" {
		t.Skip("set " + envHost + " to run the go-sql-spanner integration tests")
	}
	migratetest.Run(t, migratetest.Harness{
		OpenDB: openDB,
		Dialect: func(t *testing.T) mmmigrate.Dialect {
			return spanner.Dialect{Driver: "spanner"}
		},
		DumpSchema:    dumpSchema,
		TrackingTable: "mmmigrate_applied",
	})
}

func dumpSchema(t *testing.T, db *sql.DB) string {
	t.Helper()
	rows, err := db.Query(`
		SELECT table_schema, table_name, column_name, spanner_type, is_nullable, column_default
		FROM information_schema.columns
		WHERE table_schema NOT IN ('information_schema', 'pg_catalog', 'spanner_sys')
		ORDER BY table_schema, table_name, ordinal_position`)
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
		line := fmt.Sprintf("  %s %s", col, dtype)
		if nullable == "NO" {
			line += " NOT NULL"
		}
		if def.Valid {
			line += " DEFAULT " + def.String
		}
		key := schema + "." + table
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
	out.WriteString("-- server: spanner (PostgreSQL dialect)\n\n")
	for _, k := range keys {
		fmt.Fprintf(&out, "-- table: %s\n", k)
		for _, col := range tables[k] {
			fmt.Fprintln(&out, col)
		}
		fmt.Fprintln(&out)
	}
	return out.String()
}
