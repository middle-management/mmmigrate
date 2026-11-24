package spanner

import (
	"context"
	"database/sql"
	"regexp"
	"strconv"
	"strings"
)

// Recovery runs a statement in its idempotent form (idempotent) because the
// run it resumes may have applied it already. That is only right for a
// statement that would have succeeded as written when the migration began: a
// CREATE of an object that already existed then, or a DROP of one that
// didn't, fails as written, and must fail again rather than succeed as
// "already done" when the migration is simply run again. So when a
// migration first starts, failsAsWritten works out which of its statements
// would fail as written against the schema as it is then, following the
// migration's own creates and drops by name, and the progress row keeps
// them; recovery runs those as written.

// change is what a statement needs and does to one object: it needs the
// object absent (creates) or present (drops).
type change struct {
	key     string // kind and qualified name; see objectKey
	creates bool
	// conditional: written IF [NOT] EXISTS, or CREATE OR REPLACE. It never
	// fails, and changes nothing if the object is already as it would make it.
	conditional bool
	// columns a CREATE TABLE defines, so that ADD COLUMN after it in the
	// same migration knows them.
	columns []string
}

var (
	reCreateObj = regexp.MustCompile(`(?is)^\s*CREATE\s+(?:UNIQUE\s+)?(?:NULL_FILTERED\s+)?(TABLE|INDEX|SEQUENCE|SCHEMA|CHANGE\s+STREAM)\s+(IF\s+NOT\s+EXISTS\s+)?(` + ident + `)` + identEnd)
	reCreateV   = regexp.MustCompile(`(?is)^\s*CREATE\s+(OR\s+REPLACE\s+)?VIEW\s+(` + ident + `)` + identEnd)
	reDropObj   = regexp.MustCompile(`(?is)^\s*DROP\s+(TABLE|INDEX|SEQUENCE|SCHEMA|VIEW)\s+(IF\s+EXISTS\s+)?(` + ident + `)` + identEnd)
	reAddCol    = regexp.MustCompile(`(?is)^\s*ALTER\s+TABLE\s+(?:ONLY\s+)?(` + ident + `)\s+ADD\s+COLUMN\s+(IF\s+NOT\s+EXISTS\s+)?("(?:[^"]|"")+"|[A-Za-z_][A-Za-z0-9_$]*)` + identEnd)
)

// changeOf returns the change q makes to an object, if it is a statement
// that creates or drops one by name: those recovery rewrites, and their
// conditional forms, which change the schema later statements are judged
// against.
func changeOf(q string) (change, bool) {
	if m := reCreateObj.FindStringSubmatch(q); m != nil {
		c := change{key: objectKey(m[1], m[3]), creates: true, conditional: m[2] != ""}
		if strings.EqualFold(m[1], "TABLE") {
			c.columns = tableColumns(q)
		}
		return c, true
	}
	if m := reCreateV.FindStringSubmatch(q); m != nil {
		return change{key: objectKey("VIEW", m[2]), creates: true, conditional: m[1] != ""}, true
	}
	if m := reDropObj.FindStringSubmatch(q); m != nil {
		return change{key: objectKey(m[1], m[3]), conditional: m[2] != ""}, true
	}
	if m := reAddCol.FindStringSubmatch(q); m != nil {
		schema, table := splitIdent(m[1])
		_, col := splitIdent(m[3])
		return change{key: "column " + schema + "." + table + "." + col, creates: true, conditional: m[2] != ""}, true
	}
	return change{}, false
}

// objectKey names an object by the namespace Spanner checks names in:
// tables and views share one.
func objectKey(kind, name string) string {
	kind = strings.ToUpper(strings.Join(strings.Fields(kind), " "))
	if kind == "VIEW" {
		kind = "TABLE"
	}
	schema, n := splitIdent(name)
	if kind == "SCHEMA" {
		return "SCHEMA " + n
	}
	return kind + " " + schema + "." + n
}

// failsAsWritten returns the indexes, in the order of statements, of those
// that would fail as written if the script ran now: each statement is judged
// against the database, as changed by the script's statements before it,
// conditional ones (IF [NOT] EXISTS, OR REPLACE) included, though those never
// fail themselves.
func failsAsWritten(ctx context.Context, conn *sql.Conn, stmts []string) ([]int, error) {
	known := map[string]bool{} // objects the script created (true) or dropped (false) so far
	var fails []int
	for i, q := range stmts {
		c, ok := changeOf(q)
		if !ok {
			continue
		}
		exists, seen := known[c.key]
		if !seen {
			var err error
			if exists, err = objectExists(ctx, conn, c.key, known); err != nil {
				return nil, err
			}
		}
		if exists == c.creates {
			if c.conditional {
				// A no-op: the object stays as it is, columns and all.
				known[c.key] = exists
				continue
			}
			fails = append(fails, i)
		}
		known[c.key] = c.creates
		if strings.HasPrefix(c.key, "TABLE ") {
			// A table created or dropped here has the columns given here,
			// whatever the database's table of that name has.
			table := strings.TrimPrefix(c.key, "TABLE ")
			known["columns of "+table] = true
			for _, col := range c.columns {
				known["column "+table+"."+col] = true
			}
		}
	}
	return fails, nil
}

// objectExists looks an object up in the catalog, unless the script created
// or dropped its table, which decides its columns.
func objectExists(ctx context.Context, conn *sql.Conn, key string, known map[string]bool) (bool, error) {
	kind, name, _ := strings.Cut(key, " ")
	if kind == "column" {
		i := strings.LastIndex(name, ".")
		if known["columns of "+name[:i]] {
			return false, nil // its table is the script's: known columns are set above
		}
	}
	var q string
	var args []any
	switch kind {
	case "SCHEMA":
		q, args = `SELECT count(*) FROM information_schema.schemata WHERE schema_name = $1`, []any{name}
	case "CHANGE":
		// "CHANGE STREAM schema.name"
		_, n, _ := strings.Cut(name, " ")
		_, n, _ = strings.Cut(n, ".")
		q, args = `SELECT count(*) FROM information_schema.change_streams WHERE change_stream_name = $1`, []any{n}
	default:
		schema, n, _ := strings.Cut(name, ".")
		switch kind {
		case "TABLE":
			q = `SELECT count(*) FROM information_schema.tables WHERE table_schema = $1 AND table_name = $2`
		case "INDEX":
			q = `SELECT count(*) FROM information_schema.indexes WHERE table_schema = $1 AND index_name = $2`
		case "SEQUENCE":
			q = `SELECT count(*) FROM "information_schema"."sequences" WHERE sequence_schema = $1 AND sequence_name = $2`
		case "column":
			i := strings.LastIndex(n, ".")
			q = `SELECT count(*) FROM information_schema.columns WHERE table_schema = $1 AND table_name = $2 AND column_name = $3`
			args = []any{schema, n[:i], n[i+1:]}
		}
		if args == nil {
			args = []any{schema, n}
		}
	}
	var count int64
	if err := conn.QueryRowContext(ctx, q, args...).Scan(&count); err != nil {
		return false, err
	}
	return count > 0, nil
}

// tableColumns returns the column names a CREATE TABLE defines: the first
// word of each top-level item of its parenthesised list, constraints left
// out.
func tableColumns(q string) []string {
	open := strings.Index(q, "(")
	if open < 0 {
		return nil
	}
	var cols []string
	depth, start := 0, open+1
	item := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" {
			return
		}
		if !strings.HasPrefix(s, `"`) {
			switch strings.ToUpper(firstWord(s)) {
			case "", "CONSTRAINT", "PRIMARY", "FOREIGN", "CHECK", "UNIQUE":
				return
			}
		}
		name := s
		if strings.HasPrefix(s, `"`) {
			if end := strings.Index(s[1:], `"`); end >= 0 {
				name = s[:end+2]
			}
		} else if f := strings.Fields(s); len(f) > 0 {
			name = f[0]
		}
		_, n := splitIdent(name)
		cols = append(cols, n)
	}
	for i := open; i < len(q); i++ {
		switch q[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				item(q[start:i])
				return cols
			}
		case ',':
			if depth == 1 {
				item(q[start:i])
				start = i + 1
			}
		case '\'', '"':
			c := q[i]
			for i++; i < len(q) && q[i] != c; i++ {
			}
		}
	}
	return cols
}

func formatInts(xs []int) string {
	s := make([]string, len(xs))
	for i, x := range xs {
		s[i] = strconv.Itoa(x)
	}
	return strings.Join(s, ",")
}

func parseInts(s string) map[int]bool {
	out := map[int]bool{}
	for _, f := range strings.Split(s, ",") {
		if n, err := strconv.Atoi(strings.TrimSpace(f)); err == nil {
			out[n] = true
		}
	}
	return out
}
