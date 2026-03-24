package sqlite_test

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/middle-management/mmmigrate/driver/sqlite"
	"github.com/middle-management/mmmigrate/migrate"
	"github.com/middle-management/mmmigrate/migrate/migratetest"
)

func TestSQLite(t *testing.T) {
	migratetest.Run(t, migratetest.Harness{
		OpenDB: func(t *testing.T) *sql.DB {
			t.Helper()
			db, err := sql.Open("sqlite", ":memory:")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { db.Close() })
			return db
		},
		Dialect: func(t *testing.T) migrate.Dialect {
			return sqlite.Dialect{}
		},
		DumpSchema:    dumpSQLiteSchema,
		TrackingTable: "mmmigrate_applied",
	})
}

func dumpSQLiteSchema(t *testing.T, db *sql.DB) string {
	t.Helper()

	rows, err := db.Query(`
		SELECT type, name, sql FROM sqlite_master
		WHERE sql IS NOT NULL
		ORDER BY type, name
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	var stmts []string
	for rows.Next() {
		var typ, name, ddl string
		if err := rows.Scan(&typ, &name, &ddl); err != nil {
			t.Fatal(err)
		}
		stmts = append(stmts, fmt.Sprintf("-- %s: %s\n%s;", typ, name, ddl))
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}

	sort.Strings(stmts)
	return strings.Join(stmts, "\n\n") + "\n"
}
