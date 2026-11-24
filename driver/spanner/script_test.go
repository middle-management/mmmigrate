package spanner

import (
	"errors"
	"reflect"
	"testing"
)

func TestSplitSQL(t *testing.T) {
	got := splitSQL(`-- a comment; not a statement
CREATE TABLE a (x text DEFAULT ';'); /* b; */ INSERT INTO a VALUES ($1, 'it''s');
INSERT INTO a VALUES (E'it\'s; fine');
SELECT $tag$ ; $tag$, "semi;colon" FROM a;
`)
	want := []string{
		`CREATE TABLE a (x text DEFAULT ';')`,
		`INSERT INTO a VALUES ($1, 'it''s')`,
		`INSERT INTO a VALUES (E'it\'s; fine')`,
		`SELECT $tag$ ; $tag$, "semi;colon" FROM a`,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q\nwant %q", got, want)
	}
}

func TestPlan(t *testing.T) {
	units := plan(`CREATE TABLE a (id bigint PRIMARY KEY);
CREATE INDEX a_i ON a (id);
SET spanner.statement_tag = 'x';
INSERT INTO a VALUES (1);
ALTER TABLE a ADD COLUMN b text;
DROP INDEX a_i;
UPDATE a SET b = 'c';`)
	var kinds []kind
	var sizes []int
	for _, u := range units {
		kinds = append(kinds, u.kind)
		sizes = append(sizes, len(u.stmts))
	}
	if want := []kind{kindDDL, kindSession, kindDML, kindDDL, kindDML}; !reflect.DeepEqual(kinds, want) {
		t.Errorf("kinds %v, want %v", kinds, want)
	}
	if want := []int{2, 1, 1, 2, 1}; !reflect.DeepEqual(sizes, want) {
		t.Errorf("sizes %v, want %v", sizes, want)
	}
}

func TestIdempotent(t *testing.T) {
	for in, want := range map[string]string{
		`CREATE TABLE a (id bigint PRIMARY KEY)`:               `CREATE TABLE IF NOT EXISTS a (id bigint PRIMARY KEY)`,
		`create table if not exists a (id bigint primary key)`: `create table IF NOT EXISTS a (id bigint primary key)`,
		`CREATE UNIQUE INDEX i ON a (x)`:                       `CREATE UNIQUE INDEX IF NOT EXISTS i ON a (x)`,
		`CREATE SEQUENCE s BIT_REVERSED_POSITIVE`:              `CREATE SEQUENCE IF NOT EXISTS s BIT_REVERSED_POSITIVE`,
		`CREATE SCHEMA app`:                                    `CREATE SCHEMA IF NOT EXISTS app`,
		`CREATE VIEW v SQL SECURITY INVOKER AS SELECT 1`:       `CREATE OR REPLACE VIEW v SQL SECURITY INVOKER AS SELECT 1`,
		`DROP TABLE a`:                              `DROP TABLE IF EXISTS a`,
		`DROP INDEX i`:                              `DROP INDEX IF EXISTS i`,
		`DROP VIEW v`:                               `DROP VIEW IF EXISTS v`,
		`ALTER TABLE a ADD COLUMN b text`:           `ALTER TABLE a ADD COLUMN IF NOT EXISTS b text`,
		`ALTER TABLE "My T".x ADD COLUMN b text`:    `ALTER TABLE "My T".x ADD COLUMN IF NOT EXISTS b text`,
		`ALTER TABLE a ALTER COLUMN b SET NOT NULL`: `ALTER TABLE a ALTER COLUMN b SET NOT NULL`,
		`CREATE CHANGE STREAM cs FOR a`:             `CREATE CHANGE STREAM cs FOR a`,
		`ALTER TABLE a ADD CONSTRAINT fk FOREIGN KEY (b) REFERENCES c`: `ALTER TABLE a ADD CONSTRAINT fk FOREIGN KEY (b) REFERENCES c`,
	} {
		if got := idempotent(in); got != want {
			t.Errorf("idempotent(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSplitIdent(t *testing.T) {
	for in, want := range map[string][2]string{
		`users`:          {"public", "users"},
		`Users`:          {"public", "users"},
		`"Users"`:        {"public", "Users"},
		`app.t`:          {"app", "t"},
		`"my.app"."T x"`: {"my.app", "T x"},
	} {
		s, n := splitIdent(in)
		if [2]string{s, n} != want {
			t.Errorf("splitIdent(%q) = %q, %q, want %q", in, s, n, want)
		}
	}
}

func TestRecoveryConflict(t *testing.T) {
	for _, tc := range []struct {
		stmts []string
		ok    bool
	}{
		{[]string{`DROP TABLE t`, `CREATE TABLE t (id bigint PRIMARY KEY)`}, false},
		{[]string{`DROP TABLE IF EXISTS t`, `CREATE INDEX i ON t (id)`}, false},
		{[]string{`ALTER TABLE t DROP COLUMN c`, `ALTER TABLE t ADD COLUMN c text`}, false},
		{[]string{`DROP INDEX i`, `CREATE INDEX i ON t (x)`}, false},
		{[]string{`DROP TABLE "T"`, `CREATE TABLE "T" (id bigint PRIMARY KEY)`}, false},
		// Nothing dropped is named again: each statement re-runs safely.
		{[]string{`DROP TABLE old`, `CREATE TABLE t (id bigint PRIMARY KEY)`}, true},
		{[]string{`DROP TABLE t`, `CREATE TABLE t2 (id bigint PRIMARY KEY)`}, true},
		{[]string{`CREATE TABLE t (id bigint PRIMARY KEY)`, `DROP TABLE t`}, true},
		{[]string{`ALTER TABLE t ADD COLUMN c text`, `CREATE INDEX i ON t (c)`}, true},
		{[]string{`DROP TABLE "T"`, `CREATE TABLE t (id bigint PRIMARY KEY)`}, true},
	} {
		err := recoveryConflict(tc.stmts)
		if (err == nil) != tc.ok {
			t.Errorf("recoveryConflict(%q) = %v, want ok=%v", tc.stmts, err, tc.ok)
		}
	}
}

func TestIsTransient(t *testing.T) {
	for msg, want := range map[string]bool{
		"ERROR: Transaction was aborted. (SQLSTATE 40001)": true,
		"spanner: code = \"Aborted\", desc = \"...\"":      true,
		"ERROR: permission denied (SQLSTATE 42501)":        false,
		"sql: database is closed":                          false,
	} {
		if got := isTransient(errors.New(msg)); got != want {
			t.Errorf("isTransient(%q) = %v, want %v", msg, got, want)
		}
	}
}
