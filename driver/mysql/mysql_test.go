package mysql_test

import (
	"database/sql"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/middle-management/mmmigrate/driver/mysql"
	"github.com/middle-management/mmmigrate/migrate"
	"github.com/middle-management/mmmigrate/migrate/migratetest"
)

func TestMySQL(t *testing.T) {
	dsn := os.Getenv("MMMIGRATE_TEST_MYSQL_URL")
	if dsn == "" {
		t.Skip("set MMMIGRATE_TEST_MYSQL_URL to run mysql integration tests")
	}

	migratetest.Run(t, migratetest.Harness{
		OpenDB: func(t *testing.T) *sql.DB {
			t.Helper()
			db, err := sql.Open("mysql", dsn)
			if err != nil {
				t.Fatal(err)
			}
			// Enable multi-statement execution for ResetSQL.
			t.Cleanup(func() {
				resetDB(t, db)
				db.Close()
			})
			resetDB(t, db)
			return db
		},
		Dialect: func(t *testing.T) migrate.Dialect {
			return mysql.Dialect{}
		},
		DumpSchema:    dumpMySQLSchema,
		TrackingTable: "mmmigrate_applied",
	})
}

func resetDB(t *testing.T, db *sql.DB) {
	t.Helper()
	rows, err := db.Query("SELECT table_name FROM information_schema.tables WHERE table_schema = DATABASE()")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	var tables []string
	for rows.Next() {
		var name string
		rows.Scan(&name)
		tables = append(tables, name)
	}

	if len(tables) > 0 {
		db.Exec("SET FOREIGN_KEY_CHECKS = 0")
		for _, table := range tables {
			db.Exec("DROP TABLE IF EXISTS " + table)
		}
		db.Exec("SET FOREIGN_KEY_CHECKS = 1")
	}
}

func dumpMySQLSchema(t *testing.T, db *sql.DB) string {
	t.Helper()

	var version string
	db.QueryRow("SELECT version()").Scan(&version)

	rows, err := db.Query(`
		SELECT table_name, column_name, column_type, is_nullable, column_default
		FROM information_schema.columns
		WHERE table_schema = DATABASE()
		ORDER BY table_name, ordinal_position
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	tables := map[string][]string{}
	for rows.Next() {
		var table, col, dtype, nullable string
		var def sql.NullString
		if err := rows.Scan(&table, &col, &dtype, &nullable, &def); err != nil {
			t.Fatal(err)
		}
		line := fmt.Sprintf("  %s %s", col, dtype)
		if nullable == "NO" {
			line += " NOT NULL"
		}
		if def.Valid {
			line += " DEFAULT " + def.String
		}
		tables[table] = append(tables[table], line)
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
