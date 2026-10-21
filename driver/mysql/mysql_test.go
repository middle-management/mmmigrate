package mysql_test

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
	"github.com/middle-management/mmmigrate/driver/mysql"
	"github.com/middle-management/mmmigrate/migratetest"
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
		Dialect: func(t *testing.T) mmmigrate.Dialect {
			return mysql.Dialect{}
		},
		DumpSchema:    dumpMySQLSchema,
		TrackingTable: "mmmigrate_applied",
	})
}

// TestMySQLShadowReset checks that shadow verification starts from an empty
// database: objects of any kind left by an earlier replay must not let a
// migration pass that would fail on a fresh database.
func TestMySQLShadowReset(t *testing.T) {
	dsn := os.Getenv("MMMIGRATE_TEST_MYSQL_URL")
	if dsn == "" {
		t.Skip("set MMMIGRATE_TEST_MYSQL_URL to run mysql integration tests")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	drop := func() {
		db.Exec("DROP VIEW IF EXISTS leftover_view, v2")
		db.Exec("DROP PROCEDURE IF EXISTS leftover_proc")
		db.Exec("DROP FUNCTION IF EXISTS leftover_func")
		db.Exec("DROP EVENT IF EXISTS leftover_event")
		resetDB(t, db)
	}
	drop()
	t.Cleanup(func() { drop(); db.Close() })

	// Leftovers from an earlier replay, including a table name that needs
	// quoting.
	for _, q := range []string{
		"CREATE TABLE `leftover table` (id INT)",
		"CREATE VIEW leftover_view AS SELECT 1 AS one",
		"CREATE PROCEDURE leftover_proc() SELECT 1",
		"CREATE FUNCTION leftover_func() RETURNS INT DETERMINISTIC RETURN 1",
		"CREATE EVENT leftover_event ON SCHEDULE EVERY 1 DAY DO SELECT 1",
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}

	// A migration that depends on the leftover view without creating it.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "001_add_v2.sql"),
		[]byte("CREATE VIEW v2 AS SELECT one FROM leftover_view;"), 0644); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	if err := mmmigrate.VerifyAgainstShadow(ctx, db, mysql.Dialect{}, os.DirFS(dir)); err == nil {
		t.Error("shadow verification passed a migration that fails on a fresh database")
	}

	var leftovers []string
	rows, err := db.Query(`
		SELECT CONCAT(table_type, ' ', table_name) FROM information_schema.tables
		WHERE table_schema = DATABASE() AND table_name NOT LIKE 'mmmigrate\\_%'
		UNION ALL
		SELECT CONCAT(routine_type, ' ', routine_name) FROM information_schema.routines
		WHERE routine_schema = DATABASE()
		UNION ALL
		SELECT CONCAT('EVENT ', event_name) FROM information_schema.events
		WHERE event_schema = DATABASE()`)
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
			db.Exec("DROP TABLE IF EXISTS `" + strings.ReplaceAll(table, "`", "``") + "`")
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
