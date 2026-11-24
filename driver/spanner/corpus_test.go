package spanner

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/middle-management/mmmigrate"
)

// TestCorpus checks the statement splitter, the recovery rewrites and the
// conflict check against real migrations (testdata/corpus) on Spanner,
// rather than hand-picked strings:
//
//   - each file applies as one migration, so the splitter cut it where
//     Spanner's parser would (a mis-split statement fails);
//   - for every point a run could die at, after any DDL statement of the
//     file (Spanner keeps the statements of a batch that ran), the next run
//     resumes to the same schema and rows as a run that never died, or stops
//     with one of the driver's explicit recovery errors. It never reaches a
//     different database silently.
//
// Files named in recoverable must resume everywhere without an error.
func TestCorpus(t *testing.T) {
	dsn := os.Getenv("MMMIGRATE_TEST_SPANNER_URL")
	if dsn == "" {
		t.Skip("set MMMIGRATE_TEST_SPANNER_URL to run spanner integration tests")
	}
	recoverable := map[string]bool{"patchlog_baseline.sql": true}
	// Every crash point takes a reset and a replay: minutes for the
	// baseline's 76 statements. CI runs them all; elsewhere a sample.
	full := os.Getenv("MMMIGRATE_TEST_CORPUS_FULL") != ""

	files, err := filepath.Glob("testdata/corpus/*.sql")
	if err != nil || len(files) == 0 {
		t.Fatalf("no corpus: %v", err)
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	d := Dialect{}

	for _, file := range files {
		t.Run(filepath.Base(file), func(t *testing.T) {
			data, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			script := string(data)
			fsys := fstest.MapFS{"001_corpus.sql": {Data: data}}
			units := plan(script)

			conn, err := db.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			reset := func() {
				t.Helper()
				if err := d.Reset(ctx, conn); err != nil {
					t.Fatalf("reset: %v", err)
				}
			}

			reset()
			if err := mmmigrate.RunMigrations(ctx, db, d, fsys, false); err != nil {
				t.Fatalf("applying the file as one migration: %v", err)
			}
			want := snapshot(t, conn)

			var points, resumed, refused int
			for k, u := range units {
				if u.kind != kindDDL {
					continue
				}
				// Died before the group's first statement ran (j = 0), or
				// after its j-th.
				for j := 0; j <= len(u.stmts); j++ {
					if !full && !sampled(j, len(u.stmts)) {
						continue
					}
					points++
					at := fmt.Sprintf("group %d, after %d of %d statements", k+1, j, len(u.stmts))
					reset()
					if _, err := mmmigrate.Status(ctx, db, d, fsys); err != nil {
						t.Fatal(err)
					}
					for _, done := range units[:k] {
						if err := d.runUnit(ctx, conn, done, false); err != nil {
							t.Fatalf("%s: replaying group: %v", at, err)
						}
					}
					if j > 0 {
						if err := d.runDDL(ctx, conn, u.stmts[:j]); err != nil {
							t.Fatalf("%s: replaying statements: %v", at, err)
						}
					}
					if _, err := conn.ExecContext(ctx,
						`INSERT INTO mmmigrate_progress (namespace, version, done, prefix, updated_at) VALUES ('default', 1, $1, $2, CURRENT_TIMESTAMP)`,
						k, prefixHash(units[:k])); err != nil {
						t.Fatal(err)
					}

					err := mmmigrate.RunMigrations(ctx, db, d, fsys, false)
					switch {
					case err == nil:
						resumed++
						if got := snapshot(t, conn); got != want {
							t.Errorf("%s: resumed to a different database than an uninterrupted run:\n%s", at, diffLines(want, got))
						}
					case recoverable[filepath.Base(file)]:
						t.Errorf("%s: %v", at, err)
					case strings.Contains(err.Error(), "can't safely re-run") ||
						strings.Contains(err.Error(), "re-running a statement group an earlier run may have applied"):
						refused++
						t.Logf("%s: stopped: %.160s", at, err)
					default:
						t.Errorf("%s: failed without a recovery error: %v", at, err)
					}
				}
			}
			t.Logf("%d statements in %d groups; %d crash points: %d resumed, %d stopped for a person",
				len(splitSQL(script)), len(units), points, resumed, refused)
		})
	}
}

// snapshot describes the user schema and each table's row count, mmmigrate's
// tables left out: what a migration run leaves behind.
func snapshot(t *testing.T, conn *sql.Conn) string {
	t.Helper()
	ctx := context.Background()
	var b strings.Builder
	for _, q := range []string{
		`SELECT table_schema, table_name, coalesce(parent_table_name, ''), coalesce(on_delete_action, ''), table_type
			FROM information_schema.tables WHERE table_schema NOT IN ` + systemSchemas + ` AND table_name NOT LIKE 'mmmigrate\_%'
			ORDER BY 1, 2`,
		`SELECT table_schema, table_name, column_name, coalesce(spanner_type, ''), is_nullable, coalesce(column_default, ''), coalesce(generation_expression, '')
			FROM information_schema.columns WHERE table_schema NOT IN ` + systemSchemas + ` AND table_name NOT LIKE 'mmmigrate\_%'
			ORDER BY 1, 2, 3`,
		`SELECT table_schema, table_name, index_name, index_type, is_unique, is_null_filtered, coalesce(filter, ''), coalesce(parent_table_name, '')
			FROM information_schema.indexes WHERE table_schema NOT IN ` + systemSchemas + ` AND table_name NOT LIKE 'mmmigrate\_%' AND spanner_is_managed = 'NO'
			ORDER BY 1, 2, 3`,
		`SELECT c.table_schema, c.table_name, c.index_name, c.column_name, coalesce(c.column_ordering, ''), cast(coalesce(c.ordinal_position, 0) AS varchar)
			FROM information_schema.index_columns c JOIN information_schema.indexes i
				ON i.table_schema = c.table_schema AND i.table_name = c.table_name AND i.index_name = c.index_name
			WHERE c.table_schema NOT IN ` + systemSchemas + ` AND c.table_name NOT LIKE 'mmmigrate\_%' AND i.spanner_is_managed = 'NO'
			ORDER BY 1, 2, 3, 6, 4`,
		// Constraint names Spanner generates carry a hash: count them by kind.
		`SELECT table_schema, table_name, constraint_type, cast(count(*) AS varchar)
			FROM information_schema.table_constraints WHERE table_schema NOT IN ` + systemSchemas + ` AND table_name NOT LIKE 'mmmigrate\_%'
			GROUP BY 1, 2, 3 ORDER BY 1, 2, 3`,
		`SELECT table_schema, table_name, coalesce(view_definition, '')
			FROM information_schema.views WHERE table_schema NOT IN ` + systemSchemas + ` ORDER BY 1, 2`,
		`SELECT sequence_schema, sequence_name FROM "information_schema"."sequences" ORDER BY 1, 2`,
		`SELECT change_stream_name FROM information_schema.change_streams ORDER BY 1`,
		`SELECT schema_name FROM information_schema.schemata WHERE schema_name NOT IN ` + systemSchemas + ` ORDER BY 1`,
	} {
		rows, err := queryStrings(ctx, conn, q)
		if err != nil {
			t.Fatalf("snapshot: %v\n%s", err, q)
		}
		for _, r := range rows {
			b.WriteString(strings.Join(r, " | "))
			b.WriteByte('\n')
		}
		b.WriteString("--\n")
	}
	tables, err := queryStrings(ctx, conn, `SELECT table_schema, table_name FROM information_schema.tables
		WHERE table_type = 'BASE TABLE' AND table_schema NOT IN `+systemSchemas+` AND table_name NOT LIKE 'mmmigrate\_%' ORDER BY 1, 2`)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range tables {
		var n int64
		if err := conn.QueryRowContext(ctx, `SELECT count(*) FROM `+qualified(r[0], r[1])).Scan(&n); err != nil {
			t.Fatalf("snapshot: rows of %s: %v", r[1], err)
		}
		fmt.Fprintf(&b, "rows %s.%s %d\n", r[0], r[1], n)
	}
	return b.String()
}

func diffLines(want, got string) string {
	w, g := strings.Split(want, "\n"), strings.Split(got, "\n")
	in := func(lines []string) map[string]bool {
		m := map[string]bool{}
		for _, l := range lines {
			m[l] = true
		}
		return m
	}
	wm, gm := in(w), in(g)
	var b strings.Builder
	for _, l := range w {
		if !gm[l] {
			fmt.Fprintf(&b, "  - %s\n", l)
		}
	}
	for _, l := range g {
		if !wm[l] {
			fmt.Fprintf(&b, "  + %s\n", l)
		}
	}
	return b.String()
}

// sampled picks the crash points a quick run tries in a group of n
// statements: both ends, and every seventh between.
func sampled(j, n int) bool {
	return j <= 2 || j >= n-2 || j%7 == 0
}
