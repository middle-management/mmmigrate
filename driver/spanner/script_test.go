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
/* outer; /* nested; */ still outer; */ SELECT 1;
INSERT INTO t (v) VALUES (E'it''s\';still text', 'a''b;c', "x""y;z");
`)
	want := []string{
		`CREATE TABLE a (x text DEFAULT ';')`,
		`INSERT INTO a VALUES ($1, 'it''s')`,
		`INSERT INTO a VALUES (E'it\'s; fine')`,
		`SELECT $tag$ ; $tag$, "semi;colon" FROM a`,
		`SELECT 1`,
		`INSERT INTO t (v) VALUES (E'it''s\';still text', 'a''b;c', "x""y;z")`,
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
		`CREATE CHANGE STREAM cs FOR a`:             `CREATE CHANGE STREAM IF NOT EXISTS cs FOR a`,
		`DROP CHANGE STREAM cs`:                     `DROP CHANGE STREAM cs`,
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

// An unterminated block comment keeps its text, so the database refuses the
// statement instead of the comment silently swallowing the script's rest.
func TestSplitSQLUnterminatedComment(t *testing.T) {
	got := splitSQL("CREATE TABLE a (id bigint PRIMARY KEY); /* unfinished; CREATE TABLE b (id bigint PRIMARY KEY);")
	if len(got) != 2 || got[1] != "/* unfinished; CREATE TABLE b (id bigint PRIMARY KEY);" {
		t.Fatalf("got %q", got)
	}
}

func TestQuotedIdentifiers(t *testing.T) {
	if s, n := splitIdent(`"a""b"`); s != "public" || n != `a"b` {
		t.Errorf("splitIdent: %q %q", s, n)
	}
	// A name the patterns read only part of mustn't be taken for another
	// object: "a""b" is not "a".
	m := reIfNotExists.FindStringSubmatch(`CREATE TABLE IF NOT EXISTS "a""b" (id bigint PRIMARY KEY)`)
	if m == nil || m[2] != `"a""b"` {
		t.Errorf("reIfNotExists: %q", m)
	}
	if c, ok := changeOf(`CREATE TABLE "a""b" (id bigint PRIMARY KEY)`); !ok || c.key != `TABLE public.a"b` {
		t.Errorf("changeOf: %+v %v", c, ok)
	}
}

func TestChangeOf(t *testing.T) {
	for q, want := range map[string]string{
		`CREATE TABLE t (id bigint PRIMARY KEY)`:                    "TABLE public.t +",
		`CREATE UNIQUE INDEX i ON t (x)`:                            "INDEX public.i +",
		`CREATE SEQUENCE app.s BIT_REVERSED_POSITIVE`:               "SEQUENCE app.s +",
		`CREATE SCHEMA app`:                                         "SCHEMA app +",
		`CREATE CHANGE STREAM cs FOR t`:                             "CHANGE STREAM public.cs +",
		`CREATE VIEW v SQL SECURITY INVOKER AS SELECT 1`:            "TABLE public.v +",
		`DROP TABLE "T"`:                                            "TABLE public.T -",
		`ALTER TABLE t ADD COLUMN c text`:                           "column public.t.c +",
		`CREATE TABLE IF NOT EXISTS t (id bigint)`:                  "TABLE public.t + if",
		`DROP INDEX IF EXISTS i`:                                    "INDEX public.i - if",
		`CREATE OR REPLACE VIEW v SQL SECURITY INVOKER AS SELECT 1`: "TABLE public.v + if",
		`ALTER TABLE t ALTER COLUMN c SET NOT NULL`:                 "",
	} {
		got := ""
		if c, ok := changeOf(q); ok {
			got = c.key + map[bool]string{true: " +", false: " -"}[c.creates]
			if c.conditional {
				got += " if"
			}
		}
		if got != want {
			t.Errorf("changeOf(%q) = %q, want %q", q, got, want)
		}
	}
	cols := tableColumns(`CREATE TABLE t (id bigint PRIMARY KEY, "Name" varchar DEFAULT 'a,b', n numeric(10, 2), CONSTRAINT ck CHECK (n > 0), PRIMARY KEY (id))`)
	if want := []string{"id", "Name", "n"}; !reflect.DeepEqual(cols, want) {
		t.Errorf("tableColumns = %q, want %q", cols, want)
	}
}

// An index lives in its table's schema: CREATE INDEX i ON app.t is app.i.
func TestIndexSchema(t *testing.T) {
	for q, want := range map[string]string{
		`CREATE INDEX i ON app.t (x)`:                        "app",
		`CREATE UNIQUE INDEX IF NOT EXISTS i ON "App".t (x)`: "App",
		`CREATE INDEX i ON t (x)`:                            "public",
		`CREATE INDEX i ON t(x) INTERLEAVE IN p`:             "public",
	} {
		if got := indexSchema(q, "public"); got != want {
			t.Errorf("indexSchema(%q) = %q, want %q", q, got, want)
		}
	}
	if c, ok := changeOf(`CREATE INDEX i ON app.t (x)`); !ok || c.key != "INDEX app.i" {
		t.Errorf("changeOf: %+v %v", c, ok)
	}
}
