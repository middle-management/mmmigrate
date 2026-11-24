package spanner_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/middle-management/mmmigrate"
	"github.com/middle-management/mmmigrate/driver/spanner"
	"github.com/middle-management/mmmigrate/migratetest"
)

// The tests run against a Spanner database of the PostgreSQL dialect through
// PGAdapter, for example the emulator image that bundles both:
//
//	docker run -d -p 5432:5432 gcr.io/cloud-spanner-pg-adapter/pgadapter-emulator
//	MMMIGRATE_TEST_SPANNER_URL='postgres://x@localhost:5432/mmmigrate_test?sslmode=disable' go test ./...
//
// The emulator's PGAdapter creates a database on first connection. Every test
// resets that one database (Dialect.Reset) rather than use a new one: an
// instance holds 100 databases at most, and PGAdapter can't drop one.
const envURL = "MMMIGRATE_TEST_SPANNER_URL"

func openDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv(envURL)
	if dsn == "" {
		t.Skip("set " + envURL + " to run spanner integration tests")
	}
	db, err := sql.Open("pgx", dsn)
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

func TestSpanner(t *testing.T) {
	if os.Getenv(envURL) == "" {
		t.Skip("set " + envURL + " to run spanner integration tests")
	}
	migratetest.Run(t, migratetest.Harness{
		OpenDB: openDB,
		Dialect: func(t *testing.T) mmmigrate.Dialect {
			return spanner.Dialect{}
		},
		DumpSchema:    dumpSchema,
		TrackingTable: "mmmigrate_applied",
		// DDL can't run in a transaction at all.
		SupportsTransactionalDDL: false,
	})
}

func writeSQL(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func count(t *testing.T, db *sql.DB, q string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return n
}

// A migration that fails part-way leaves the statements before the failure
// applied, since DDL can't be rolled back, and isn't recorded. Once the
// failing statement is fixed, the next run resumes: the DML already applied
// doesn't run again, and the DDL batch that failed part-way is re-run in its
// idempotent form.
func TestSpannerResumeAfterFailure(t *testing.T) {
	db := openDB(t)
	ctx := context.Background()
	d := spanner.Dialect{}
	dir := t.TempDir()
	fsys := os.DirFS(dir)

	head := `CREATE TABLE a (id bigint PRIMARY KEY, n bigint);
CREATE INDEX a_n ON a (n);
INSERT INTO a (id, n) VALUES (1, 1);
UPDATE a SET n = n + 1 WHERE id = 1;
CREATE TABLE b (id bigint PRIMARY KEY);
ALTER TABLE a ADD COLUMN extra text;
`
	writeSQL(t, dir, "001_broken.sql", head+`CREATE TABLE a (id bigint PRIMARY KEY);`)
	if err := mmmigrate.RunMigrations(ctx, db, d, fsys, false); err == nil {
		t.Fatal("expected the duplicate table to fail the migration")
	}
	if n := count(t, db, `SELECT count(*) FROM mmmigrate_applied WHERE namespace = 'default'`); n != 0 {
		t.Fatalf("failed migration recorded as applied (%d rows)", n)
	}
	// Spanner keeps the statements of the batch before the one that failed
	// (table b); the emulator rolls the whole batch back. Either way the
	// re-run below must end with b.
	if n := count(t, db, `SELECT done FROM mmmigrate_progress WHERE namespace = 'default' AND version = 1`); n != 3 {
		t.Fatalf("progress: done = %d, want 3 (the first DDL batch and both DML statements)", n)
	}

	// The developer fixes the last statement.
	writeSQL(t, dir, "001_broken.sql", head+`CREATE TABLE c (id bigint PRIMARY KEY);`)
	if err := mmmigrate.RunMigrations(ctx, db, d, fsys, false); err != nil {
		t.Fatal(err)
	}
	if n := count(t, db, `SELECT n FROM a WHERE id = 1`); n != 2 {
		t.Errorf("a.n = %d, want 2: the DML ran again", n)
	}
	if n := count(t, db, `SELECT count(*) FROM information_schema.tables WHERE table_name IN ('b', 'c')`); n != 2 {
		t.Errorf("expected tables b and c, got %d", n)
	}
	if n := count(t, db, `SELECT count(*) FROM mmmigrate_applied WHERE namespace = 'default' AND version = 1`); n != 1 {
		t.Errorf("migration not recorded")
	}
	if n := count(t, db, `SELECT count(*) FROM mmmigrate_progress`); n != 0 {
		t.Errorf("progress rows left: %d", n)
	}
}

// A run that dies after a migration's DDL took effect but before it was
// counted or recorded: the next run re-runs that DDL in its idempotent form
// and records the migration.
func TestSpannerResumeAfterCrash(t *testing.T) {
	db := openDB(t)
	ctx := context.Background()
	d := spanner.Dialect{}
	dir := t.TempDir()
	fsys := os.DirFS(dir)

	// Bring the tracking tables up, then stage the crashed run: its progress
	// row (written before the first statement ran) and the effects of its
	// DDL.
	if _, err := mmmigrate.Status(ctx, db, d, fsys); err != nil {
		t.Fatal(err)
	}
	// The prefix hash of no statement groups is SHA-256 of nothing.
	if _, err := db.Exec(`INSERT INTO mmmigrate_progress (namespace, version, done, prefix, updated_at)
		VALUES ('default', 1, 0, 'e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855', CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`CREATE TABLE t (id bigint PRIMARY KEY)`,
		`CREATE INDEX t_id ON t (id)`,
		`CREATE SEQUENCE s BIT_REVERSED_POSITIVE`,
		`CREATE VIEW v SQL SECURITY INVOKER AS SELECT id FROM t`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	writeSQL(t, dir, "001_crashed.sql", `CREATE TABLE t (id bigint PRIMARY KEY);
CREATE INDEX t_id ON t (id);
CREATE SEQUENCE s BIT_REVERSED_POSITIVE;
CREATE VIEW v SQL SECURITY INVOKER AS SELECT id FROM t;
ALTER TABLE t ADD COLUMN note text;
`)
	if err := mmmigrate.RunMigrations(ctx, db, d, fsys, false); err != nil {
		t.Fatal(err)
	}
	if n := count(t, db, `SELECT count(*) FROM information_schema.columns WHERE table_name = 't' AND column_name = 'note'`); n != 1 {
		t.Errorf("column note missing")
	}
	if n := count(t, db, `SELECT count(*) FROM mmmigrate_applied WHERE namespace = 'default'`); n != 1 {
		t.Errorf("migration not recorded")
	}
}

// A partly applied migration whose applied statements were since changed is
// refused: the database may not match either version.
func TestSpannerResumeRefusesChangedPrefix(t *testing.T) {
	db := openDB(t)
	ctx := context.Background()
	d := spanner.Dialect{}
	dir := t.TempDir()
	fsys := os.DirFS(dir)

	writeSQL(t, dir, "001_m.sql", `CREATE TABLE a (id bigint PRIMARY KEY); SELECT * FROM no_such_table;`)
	if err := mmmigrate.RunMigrations(ctx, db, d, fsys, false); err == nil {
		t.Fatal("expected failure")
	}
	writeSQL(t, dir, "001_m.sql", `CREATE TABLE a2 (id bigint PRIMARY KEY); SELECT 1;`)
	err := mmmigrate.RunMigrations(ctx, db, d, fsys, false)
	if err == nil || !strings.Contains(err.Error(), "partly applied by an earlier run") {
		t.Fatalf("expected the changed migration refused, got %v", err)
	}
}

// The lease admits one holder per namespace; a second waits until it is
// released, and other namespaces don't wait.
func TestSpannerLease(t *testing.T) {
	db := openDB(t)
	ctx := context.Background()
	d := spanner.Dialect{LeaseTTL: 3 * time.Second}

	conn1, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn1.Close()
	_, release1, err := d.AcquireLock(ctx, db, conn1, "ns")
	if err != nil {
		t.Fatal(err)
	}

	conn2, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn2.Close()

	// Another namespace isn't blocked.
	_, releaseOther, err := d.AcquireLock(ctx, db, conn2, "other")
	if err != nil {
		t.Fatal(err)
	}
	releaseOther()

	// The same namespace waits, past the TTL, since the holder renews.
	var got atomic.Bool
	acquired := make(chan func())
	go func() {
		_, release2, err := d.AcquireLock(ctx, db, conn2, "ns")
		if err != nil {
			t.Error(err)
			close(acquired)
			return
		}
		got.Store(true)
		acquired <- release2
	}()
	time.Sleep(2 * d.LeaseTTL)
	if got.Load() {
		t.Fatal("second run took a lease the first holds and renews")
	}
	release1()
	select {
	case release2 := <-acquired:
		if release2 != nil {
			release2()
		}
	case <-time.After(10 * time.Second):
		t.Fatal("second run didn't take the released lease")
	}
}

// A run whose lease expired unrenewed and was taken by another sees its
// context cancelled at the next renewal.
func TestSpannerLeaseLost(t *testing.T) {
	db := openDB(t)
	ctx := context.Background()
	d := spanner.Dialect{LeaseTTL: 1500 * time.Millisecond}

	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	lctx, release, err := d.AcquireLock(ctx, db, conn, "ns")
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	// Another run took it over (as if this one had stalled past expiry).
	if _, err := db.Exec(`UPDATE mmmigrate_lock SET owner = 'someone-else' WHERE namespace = 'ns'`); err != nil {
		t.Fatal(err)
	}
	select {
	case <-lctx.Done():
		if !errors.Is(context.Cause(lctx), spanner.ErrLeaseLost) {
			t.Fatalf("cause %v, want ErrLeaseLost", context.Cause(lctx))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("lost lease not noticed")
	}
}

// A lease left by a run that died is taken over once it expires.
func TestSpannerLeaseExpires(t *testing.T) {
	db := openDB(t)
	ctx := context.Background()
	d := spanner.Dialect{LeaseTTL: 2 * time.Second}

	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	// Take and release once so the table and row exist, then stage a lease
	// held by a dead run.
	_, release, err := d.AcquireLock(ctx, db, conn, "ns")
	if err != nil {
		t.Fatal(err)
	}
	release()
	if _, err := db.Exec(`UPDATE mmmigrate_lock SET owner = 'dead', expires_at = spanner.timestamptz_add(CURRENT_TIMESTAMP, '2 seconds') WHERE namespace = 'ns'`); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	_, release, err = d.AcquireLock(ctx, db, conn, "ns")
	if err != nil {
		t.Fatal(err)
	}
	release()
	if waited := time.Since(start); waited < time.Second {
		t.Errorf("took a live lease after %v", waited)
	}
}

// The reset leaves nothing behind that would let a migration pass on the
// shadow which fails on a fresh database.
func TestSpannerShadowReset(t *testing.T) {
	db := openDB(t)
	ctx := context.Background()

	for _, q := range []string{
		`CREATE SCHEMA app`,
		`CREATE TABLE app.z (id bigint PRIMARY KEY)`,
		`CREATE TABLE parent (id bigint PRIMARY KEY)`,
		`CREATE TABLE child (id bigint, k bigint, PRIMARY KEY (id, k)) INTERLEAVE IN PARENT parent`,
		`CREATE TABLE "MixedCase" (id bigint PRIMARY KEY, p bigint REFERENCES parent (id))`,
		`CREATE INDEX child_k ON child (k)`,
		`CREATE UNIQUE INDEX z_id ON app.z (id)`,
		`CREATE VIEW leftover_view SQL SECURITY INVOKER AS SELECT id FROM parent`,
		`CREATE CHANGE STREAM leftover_stream FOR parent`,
		`CREATE SEQUENCE leftover_seq BIT_REVERSED_POSITIVE`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}

	dir := t.TempDir()
	writeSQL(t, dir, "001_add_v2.sql", `CREATE VIEW v2 SQL SECURITY INVOKER AS SELECT id FROM leftover_view;`)
	if err := mmmigrate.VerifyAgainstShadow(ctx, db, spanner.Dialect{}, os.DirFS(dir)); err == nil {
		t.Error("shadow verification passed a migration that fails on a fresh database")
	}

	var left []string
	rows, err := db.Query(`
		SELECT table_schema || '.' || table_name FROM information_schema.tables
			WHERE table_schema NOT IN ('information_schema', 'pg_catalog', 'spanner_sys') AND table_name NOT LIKE 'mmmigrate\_%'
		UNION ALL SELECT 'schema ' || schema_name FROM information_schema.schemata
			WHERE schema_name NOT IN ('information_schema', 'pg_catalog', 'spanner_sys', 'public')
		UNION ALL SELECT 'stream ' || change_stream_name FROM information_schema.change_streams
		UNION ALL SELECT 'sequence ' || sequence_name FROM "information_schema"."sequences"`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		left = append(left, s)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(left) > 0 {
		t.Errorf("reset left objects behind: %v", left)
	}
}

// A script mixing DDL, DML and session statements applies through
// RunMigrations, with current.sql re-run when it changes.
func TestSpannerMixedScript(t *testing.T) {
	db := openDB(t)
	ctx := context.Background()
	dir := t.TempDir()
	writeSQL(t, dir, "001_mixed.sql", `-- a comment; with a semicolon
CREATE TABLE kv (k varchar PRIMARY KEY, v varchar);
CREATE INDEX kv_v ON kv (v);
INSERT INTO kv (k, v) VALUES ('a', 'x;y'), ('b', $$it's$$);
CREATE TABLE kv2 (k varchar PRIMARY KEY);
INSERT INTO kv2 (k) SELECT k FROM kv;
`)
	writeSQL(t, dir, "current.sql", `CREATE TABLE IF NOT EXISTS extra (id bigint PRIMARY KEY);`)
	fsys := os.DirFS(dir)
	if err := mmmigrate.RunMigrations(ctx, db, spanner.Dialect{}, fsys, true); err != nil {
		t.Fatal(err)
	}
	if n := count(t, db, `SELECT count(*) FROM kv2`); n != 2 {
		t.Errorf("kv2 has %d rows, want 2", n)
	}
	var v string
	if err := db.QueryRow(`SELECT v FROM kv WHERE k = 'b'`).Scan(&v); err != nil || v != "it's" {
		t.Errorf("kv b = %q (%v)", v, err)
	}
	writeSQL(t, dir, "current.sql", `CREATE TABLE IF NOT EXISTS extra (id bigint PRIMARY KEY);
CREATE TABLE IF NOT EXISTS extra2 (id bigint PRIMARY KEY);`)
	if err := mmmigrate.RunMigrations(ctx, db, spanner.Dialect{}, fsys, true); err != nil {
		t.Fatal(err)
	}
	if n := count(t, db, `SELECT count(*) FROM information_schema.tables WHERE table_name IN ('extra', 'extra2')`); n != 2 {
		t.Errorf("current.sql tables: %d, want 2", n)
	}
}

// dumpSchema lists the user tables' columns, as information_schema has them.
func dumpSchema(t *testing.T, db *sql.DB) string {
	t.Helper()

	rows, err := db.Query(`
		SELECT table_schema, table_name, column_name, spanner_type, is_nullable, column_default
		FROM information_schema.columns
		WHERE table_schema NOT IN ('information_schema', 'pg_catalog', 'spanner_sys')
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
	fmt.Fprintf(&out, "-- server: spanner (PostgreSQL dialect)\n\n")
	for _, k := range keys {
		fmt.Fprintf(&out, "-- table: %s\n", k)
		for _, col := range tables[k] {
			fmt.Fprintln(&out, col)
		}
		fmt.Fprintln(&out)
	}
	return out.String()
}

// A batch that drops an object and creates it again with IF NOT EXISTS
// recreates it: the existence check must not skip a CREATE that an earlier
// statement of the batch makes necessary.
func TestSpannerBatchDropThenCreateIfNotExists(t *testing.T) {
	db := openDB(t)
	ctx := context.Background()
	dir := t.TempDir()
	writeSQL(t, dir, "001_t.sql", `CREATE TABLE t (id bigint PRIMARY KEY);`)
	writeSQL(t, dir, "002_rebuild.sql", `DROP TABLE t; CREATE TABLE IF NOT EXISTS t (id bigint PRIMARY KEY, note varchar);`)
	if err := mmmigrate.RunMigrations(ctx, db, spanner.Dialect{}, os.DirFS(dir), false); err != nil {
		t.Fatal(err)
	}
	if n := count(t, db, `SELECT count(*) FROM information_schema.columns WHERE table_name = 't' AND column_name = 'note'`); n != 1 {
		t.Fatalf("t wasn't recreated (note columns: %d)", n)
	}
}

// A statement group that drops what a later statement of it creates isn't
// re-run automatically after an interrupted run: if it had completed, the
// re-run would drop the new table and the rows written to it since.
func TestSpannerRecoveryRefusesDropThenCreate(t *testing.T) {
	db := openDB(t)
	ctx := context.Background()
	d := spanner.Dialect{}
	dir := t.TempDir()
	fsys := os.DirFS(dir)
	if _, err := mmmigrate.Status(ctx, db, d, fsys); err != nil {
		t.Fatal(err)
	}
	// The crashed run: its progress row, and the group's effect (the new t,
	// already holding a row).
	for _, q := range []string{
		`INSERT INTO mmmigrate_progress (namespace, version, done, prefix, updated_at)
			VALUES ('default', 1, 0, 'e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855', CURRENT_TIMESTAMP)`,
		`CREATE TABLE t (id bigint PRIMARY KEY)`,
		`INSERT INTO t (id) VALUES (1)`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	writeSQL(t, dir, "001_rebuild.sql", `DROP TABLE IF EXISTS t; CREATE TABLE t (id bigint PRIMARY KEY);`)
	err := mmmigrate.RunMigrations(ctx, db, d, fsys, false)
	if err == nil || !strings.Contains(err.Error(), "can't safely re-run") || !strings.Contains(err.Error(), "SET done = 1, prefix = '") {
		t.Fatalf("expected the re-run refused with how to mark it done, got %v", err)
	}
	if n := count(t, db, `SELECT count(*) FROM t`); n != 1 {
		t.Fatalf("t has %d rows: the re-run dropped it", n)
	}
}

// A holder whose renewals can't be confirmed stops before its lease could
// expire, rather than go on while another run may take it.
func TestSpannerLeaseUnrenewed(t *testing.T) {
	db := openDB(t)
	ctx := context.Background()
	d := spanner.Dialect{LeaseTTL: 1500 * time.Millisecond}
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	// Renewals go through a pool that can't serve them.
	dead, err := sql.Open("pgx", os.Getenv(envURL))
	if err != nil {
		t.Fatal(err)
	}
	dead.Close()
	lctx, release, err := d.AcquireLock(ctx, dead, conn, "ns")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	select {
	case <-lctx.Done():
		if !errors.Is(context.Cause(lctx), spanner.ErrLeaseLost) {
			t.Fatalf("cause %v, want ErrLeaseLost", context.Cause(lctx))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the holder went on past its lease's expiry")
	}
}

// A run whose lease was taken over can't count or record anything, even
// before its renewal notices: the counted transactions check the lease.
func TestSpannerLeaseFencesWrites(t *testing.T) {
	db := openDB(t)
	ctx := context.Background()
	d := spanner.Dialect{}
	if _, err := mmmigrate.Status(ctx, db, d, os.DirFS(t.TempDir())); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE t (id bigint PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	lctx, release, err := d.AcquireLock(ctx, db, conn, "default")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if _, err := db.Exec(`UPDATE mmmigrate_lock SET owner = 'someone-else' WHERE namespace = 'default'`); err != nil {
		t.Fatal(err)
	}
	err = d.ApplyMigration(lctx, conn, "default", 1, "fill", `INSERT INTO t (id) VALUES (1);`)
	if !errors.Is(err, spanner.ErrLeaseLost) {
		t.Fatalf("ApplyMigration under a lost lease: %v, want ErrLeaseLost", err)
	}
	if n := count(t, db, `SELECT count(*) FROM t`); n != 0 {
		t.Fatalf("the insert committed under a lost lease")
	}
	if n := count(t, db, `SELECT count(*) FROM mmmigrate_applied WHERE namespace = 'default'`); n != 0 {
		t.Fatalf("recorded under a lost lease")
	}
}
