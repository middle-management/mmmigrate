package spanner

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strings"
)

type kind int

const (
	kindDDL     kind = iota // schema changes: run outside transactions, batched
	kindDML                 // everything else that reads or writes rows
	kindSession             // session statements (SET, RESET, SHOW): no transaction, no progress
)

// unit is a step of a script: a batch of consecutive DDL statements, or one
// other statement.
type unit struct {
	kind  kind
	stmts []string
}

// plan splits script into statements and groups them into units.
func plan(script string) []unit {
	var units []unit
	for _, q := range splitSQL(script) {
		k := classify(q)
		if k == kindDDL && len(units) > 0 && units[len(units)-1].kind == kindDDL {
			last := &units[len(units)-1]
			last.stmts = append(last.stmts, q)
			continue
		}
		units = append(units, unit{kind: k, stmts: []string{q}})
	}
	return units
}

func classify(q string) kind {
	w := strings.ToUpper(firstWord(q))
	switch w {
	case "CREATE", "ALTER", "DROP", "ANALYZE", "GRANT", "REVOKE", "COMMENT", "RENAME":
		return kindDDL
	case "SET", "RESET", "SHOW":
		return kindSession
	}
	return kindDML
}

func firstWord(q string) string {
	q = strings.TrimLeft(q, " \t\r\n(")
	end := strings.IndexFunc(q, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r == '_')
	})
	if end < 0 {
		return q
	}
	return q[:end]
}

// splitSQL splits a script into its statements, without comments: at
// semicolons outside quotes, quoted identifiers, dollar quotes and comments.
// Neither driver runs a script of several statements as PostgreSQL does:
// go-sql-spanner takes one statement per call, and PGAdapter runs DDL only
// outside transactions.
func splitSQL(s string) []string {
	var out []string
	var b strings.Builder
	flush := func() {
		if q := strings.TrimSpace(b.String()); q != "" {
			out = append(out, q)
		}
		b.Reset()
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '-' && i+1 < len(s) && s[i+1] == '-':
			for i < len(s) && s[i] != '\n' {
				i++
			}
			b.WriteByte('\n')
		case c == '/' && i+1 < len(s) && s[i+1] == '*':
			// Block comments nest, as in PostgreSQL.
			start := i
			depth := 1
			i += 2
			for i < len(s) && depth > 0 {
				switch {
				case s[i] == '/' && i+1 < len(s) && s[i+1] == '*':
					depth++
					i += 2
				case s[i] == '*' && i+1 < len(s) && s[i+1] == '/':
					depth--
					i += 2
				default:
					i++
				}
			}
			if depth > 0 {
				// Unterminated: keep the text, so the database refuses the
				// statement rather than the comment swallow what follows.
				b.WriteString(s[start:])
				break
			}
			i-- // the loop's i++ moves past the comment
			b.WriteByte(' ')
		case c == '\'' || c == '"':
			// A doubled quote stands for one, inside the same literal; E'...'
			// strings also escape with a backslash, throughout.
			escapes := c == '\'' && i > 0 && (s[i-1] == 'E' || s[i-1] == 'e') && (i < 2 || !isIdentByte(s[i-2]))
			j := i + 1
			for j < len(s) {
				if escapes && s[j] == '\\' {
					j += 2
					continue
				}
				if s[j] == c {
					if j+1 < len(s) && s[j+1] == c {
						j += 2
						continue
					}
					break
				}
				j++
			}
			b.WriteString(s[i:min(j+1, len(s))])
			i = j
		case c == '$' && (i == 0 || !isIdentByte(s[i-1])):
			j := i + 1
			for j < len(s) && (s[j] == '_' || s[j] >= 'a' && s[j] <= 'z' || s[j] >= 'A' && s[j] <= 'Z' || j > i+1 && s[j] >= '0' && s[j] <= '9') {
				j++
			}
			if j < len(s) && s[j] == '$' {
				tag := s[i : j+1]
				end := strings.Index(s[j+1:], tag)
				if end < 0 {
					end = len(s) - j - 1
				} else {
					end += len(tag)
				}
				b.WriteString(s[i:min(j+1+end, len(s))])
				i = j + end
			} else {
				b.WriteByte(c)
			}
		case c == ';':
			flush()
		default:
			b.WriteByte(c)
		}
	}
	flush()
	return out
}

func isIdentByte(c byte) bool {
	return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

// Statement shapes the recovery rewrites and the existence check recognise.
// An identifier is a plain or double-quoted name, optionally schema-qualified.
const ident = `(?:"(?:[^"]|"")+"|[A-Za-z_][A-Za-z0-9_$]*)(?:\.(?:"(?:[^"]|"")+"|[A-Za-z_][A-Za-z0-9_$]*))?`

// identEnd is what may follow an identifier in the statements matched here.
// Requiring it keeps a pattern from matching part of a name it doesn't
// understand, which would then be taken for a different object.
const identEnd = `(?:\s|\(|;|$)`

var (
	reCreate      = regexp.MustCompile(`(?is)^(\s*CREATE\s+(?:UNIQUE\s+)?(?:NULL_FILTERED\s+)?(TABLE|INDEX|SEQUENCE|SCHEMA|SEARCH\s+INDEX|CHANGE\s+STREAM))\s+(?:IF\s+NOT\s+EXISTS\s+)?`)
	reCreateView  = regexp.MustCompile(`(?is)^(\s*CREATE)\s+(?:OR\s+REPLACE\s+)?(VIEW)\s`)
	reDrop        = regexp.MustCompile(`(?is)^(\s*DROP\s+(TABLE|INDEX|SEQUENCE|SCHEMA|VIEW|SEARCH\s+INDEX|CHANGE\s+STREAM))\s+(?:IF\s+EXISTS\s+)?`)
	reAddColumn   = regexp.MustCompile(`(?is)^(\s*ALTER\s+TABLE\s+(?:ONLY\s+)?` + ident + `\s+ADD\s+COLUMN)\s+(?:IF\s+NOT\s+EXISTS\s+)?`)
	reIfNotExists = regexp.MustCompile(`(?is)^\s*CREATE\s+(?:UNIQUE\s+)?(?:NULL_FILTERED\s+)?(TABLE|INDEX)\s+IF\s+NOT\s+EXISTS\s+(` + ident + `)` + identEnd)
)

// idempotent rewrites a DDL statement into a form that succeeds, changing
// nothing, if it already took effect: the forms Spanner offers for it.
// Statements without one are returned unchanged.
func idempotent(q string) string {
	if m := reCreateView.FindStringSubmatchIndex(q); m != nil {
		return q[:m[3]] + " OR REPLACE " + q[m[4]:]
	}
	for _, re := range []struct {
		re   *regexp.Regexp
		with string
	}{{reCreate, " IF NOT EXISTS "}, {reDrop, " IF EXISTS "}, {reAddColumn, " IF NOT EXISTS "}} {
		if m := re.re.FindStringSubmatchIndex(q); m != nil {
			// Spanner has CREATE CHANGE STREAM IF NOT EXISTS, but no IF
			// EXISTS for dropping one, nor either for search indexes: leave
			// those as written.
			if len(m) > 4 && m[4] >= 0 {
				kind := strings.ToUpper(strings.Join(strings.Fields(q[m[4]:m[5]]), " "))
				if kind == "SEARCH INDEX" || kind == "CHANGE STREAM" && re.re == reDrop {
					return q
				}
			}
			return q[:m[3]] + re.with + q[m[1]:]
		}
	}
	return q
}

// alreadyExists reports whether q is CREATE TABLE or CREATE INDEX ... IF NOT
// EXISTS of a table or index that exists, so running it would change
// nothing. It says no if any of before, the statements to run ahead of q,
// names the object: they may drop or change it.
func alreadyExists(ctx context.Context, conn *sql.Conn, q string, before []string) (bool, error) {
	m := reIfNotExists.FindStringSubmatch(q)
	if m == nil {
		return false, nil
	}
	schema, name := splitIdent(m[2])
	if !strings.EqualFold(m[1], "TABLE") {
		schema = indexSchema(q, schema)
	}
	for _, b := range before {
		if mentions(b, name) {
			return false, nil
		}
	}
	var query string
	if strings.EqualFold(m[1], "TABLE") {
		query = `SELECT count(*) FROM information_schema.tables WHERE table_schema = $1 AND table_name = $2`
	} else {
		query = `SELECT count(*) FROM information_schema.indexes WHERE table_schema = $1 AND index_name = $2`
	}
	var n int64
	if err := conn.QueryRowContext(ctx, query, schema, name).Scan(&n); err != nil {
		return false, err
	}
	return n > 0, nil
}

// splitIdent splits a possibly schema-qualified identifier as Spanner's
// information_schema names it: unquoted names are case-sensitive on Spanner
// but folded to lower case by its PostgreSQL dialect, as by PostgreSQL.
func splitIdent(s string) (schema, name string) {
	schema = "public"
	parts := []string{s}
	if i := identDot(s); i >= 0 {
		parts = []string{s[:i], s[i+1:]}
	}
	unq := func(p string) string {
		if len(p) >= 2 && p[0] == '"' && p[len(p)-1] == '"' {
			return strings.ReplaceAll(p[1:len(p)-1], `""`, `"`)
		}
		return strings.ToLower(p)
	}
	if len(parts) == 2 {
		return unq(parts[0]), unq(parts[1])
	}
	return schema, unq(parts[0])
}

func identDot(s string) int {
	quoted := false
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '"':
			quoted = !quoted
		case '.':
			if !quoted {
				return i
			}
		}
	}
	return -1
}

// quoteIdent quotes a name for DDL.
func quoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

var (
	reDropObject = regexp.MustCompile(`(?is)^\s*DROP\s+(?:TABLE|INDEX|SEQUENCE|SCHEMA|VIEW|SEARCH\s+INDEX|CHANGE\s+STREAM)\s+(?:IF\s+EXISTS\s+)?(` + ident + `)`)
	reDropPart   = regexp.MustCompile(`(?is)\bDROP\s+(?:COLUMN|CONSTRAINT)\s+(?:IF\s+EXISTS\s+)?("[^"]+"|[A-Za-z_][A-Za-z0-9_$]*)`)
)

// dropped returns the names of what q removes: a DROP statement's object, or
// the columns and constraints an ALTER TABLE drops.
func dropped(q string) []string {
	if m := reDropObject.FindStringSubmatch(q); m != nil {
		_, name := splitIdent(m[1])
		return []string{name}
	}
	if strings.EqualFold(firstWord(q), "ALTER") {
		var names []string
		for _, m := range reDropPart.FindAllStringSubmatch(q, -1) {
			_, name := splitIdent(m[1])
			names = append(names, name)
		}
		return names
	}
	return nil
}

// mentions reports whether q names name as an identifier: case-insensitively
// unless the name has upper-case letters (then it was quoted, and matches as
// written).
func mentions(q, name string) bool {
	pattern := `(^|[^A-Za-z0-9_$])"?` + regexp.QuoteMeta(name) + `"?($|[^A-Za-z0-9_$])`
	if name == strings.ToLower(name) {
		pattern = "(?i)" + pattern
	}
	return regexp.MustCompile(pattern).MatchString(q)
}

// recoveryConflict refuses to re-run a statement group, statement by
// statement in idempotent form, when one statement drops something a later
// one names. If the group had completed before the run that applied it
// died, the drop would take out what the later statement made, with any rows
// written to it since: DROP TABLE t; CREATE TABLE t (...), or ALTER TABLE t
// DROP COLUMN c; ALTER TABLE t ADD COLUMN c. Such a group needs a person to
// say how far it got.
func recoveryConflict(stmts []string) error {
	for i, q := range stmts {
		for _, name := range dropped(q) {
			for _, later := range stmts[i+1:] {
				if mentions(later, name) {
					return fmt.Errorf("can't safely re-run this statement group: %q drops %s, which a later statement of the group (%q) names, "+
						"so re-running it after it completed would drop what that statement made", firstLine(q), name, firstLine(later))
				}
			}
		}
	}
	return nil
}

// reIndexOn finds the table an index statement is ON.
var reIndexOn = regexp.MustCompile(`(?is)\sON\s+(?:ONLY\s+)?(` + ident + `)` + identEnd)

// indexSchema is the schema an index of CREATE INDEX q lives in: its table's.
// Spanner's PostgreSQL dialect takes no schema in the index name and puts the
// index beside its table, and information_schema.indexes lists it there.
func indexSchema(q, fallback string) string {
	if m := reIndexOn.FindStringSubmatch(q); m != nil {
		schema, _ := splitIdent(m[1])
		return schema
	}
	return fallback
}
