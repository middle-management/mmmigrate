package spanner

import (
	"context"
	"database/sql"
	"fmt"
)

// systemSchemas are the schemas Spanner's PostgreSQL dialect provides.
const systemSchemas = `('information_schema', 'pg_catalog', 'spanner_sys')`

// Reset drops every user object in the database, mmmigrate's tracking tables
// included, in the order Spanner requires: views (they depend on tables, and
// may depend on each other),
// change streams, indexes the user created (Spanner refuses to drop a table
// that has any), foreign keys (and with them the indexes Spanner manages for
// them), tables (interleaved children before their parents), sequences, and
// named schemas. Each step is one DDL batch.
//
// Sequences are listed from "information_schema"."sequences", quoted:
// PGAdapter replaces the unquoted name with an empty stand-in.
func (d Dialect) Reset(ctx context.Context, conn *sql.Conn) error {
	steps := []struct {
		what  string
		query string
		drop  func(row []string) string
	}{
		{"change streams", `SELECT change_stream_name FROM information_schema.change_streams`,
			func(r []string) string { return "DROP CHANGE STREAM " + quoteIdent(r[0]) }},
		{"indexes", `SELECT table_schema, index_name, index_type FROM information_schema.indexes
				WHERE table_schema NOT IN ` + systemSchemas + ` AND index_type <> 'PRIMARY_KEY' AND spanner_is_managed = 'NO'`,
			func(r []string) string {
				if r[2] == "SEARCH" {
					return "DROP SEARCH INDEX " + qualified(r[0], r[1])
				}
				return "DROP INDEX " + qualified(r[0], r[1])
			}},
		{"foreign keys", `SELECT table_schema, table_name, constraint_name FROM information_schema.table_constraints
				WHERE constraint_type = 'FOREIGN KEY' AND table_schema NOT IN ` + systemSchemas,
			func(r []string) string {
				return "ALTER TABLE " + qualified(r[0], r[1]) + " DROP CONSTRAINT " + quoteIdent(r[2])
			}},
		{"sequences", `SELECT sequence_schema, sequence_name FROM "information_schema"."sequences"`,
			func(r []string) string { return "DROP SEQUENCE " + qualified(r[0], r[1]) }},
	}

	if err := d.dropViews(ctx, conn); err != nil {
		return err
	}
	for i, step := range steps {
		if step.what == "sequences" {
			// Tables before sequences: a column default may use one.
			if err := d.dropTables(ctx, conn); err != nil {
				return err
			}
		}
		rows, err := queryStrings(ctx, conn, step.query)
		if err != nil {
			return fmt.Errorf("list %s: %w", step.what, err)
		}
		var drops []string
		for _, r := range rows {
			drops = append(drops, step.drop(r))
		}
		if err := d.runDDL(ctx, conn, drops); err != nil {
			return fmt.Errorf("drop %s: %w", steps[i].what, err)
		}
	}

	schemas, err := queryStrings(ctx, conn, `SELECT schema_name FROM information_schema.schemata
		WHERE schema_name NOT IN `+systemSchemas+` AND schema_name <> 'public'`)
	if err != nil {
		return fmt.Errorf("list schemas: %w", err)
	}
	var drops []string
	for _, r := range schemas {
		drops = append(drops, "DROP SCHEMA "+quoteIdent(r[0]))
	}
	if err := d.runDDL(ctx, conn, drops); err != nil {
		return fmt.Errorf("drop schemas: %w", err)
	}
	return nil
}

// dropTables drops every base table, each interleaved child before its
// parent.
func (d Dialect) dropTables(ctx context.Context, conn *sql.Conn) error {
	rows, err := queryStrings(ctx, conn, `SELECT table_schema, table_name, coalesce(parent_table_name, '')
		FROM information_schema.tables WHERE table_type = 'BASE TABLE' AND table_schema NOT IN `+systemSchemas)
	if err != nil {
		return fmt.Errorf("list tables: %w", err)
	}
	children := map[string][]string{} // parent -> children, schema-qualified
	var all []string
	for _, r := range rows {
		t := qualified(r[0], r[1])
		all = append(all, t)
		if r[2] != "" {
			p := qualified(r[0], r[2])
			children[p] = append(children[p], t)
		}
	}
	var order []string
	seen := map[string]bool{}
	var visit func(t string)
	visit = func(t string) {
		if seen[t] {
			return
		}
		seen[t] = true
		for _, c := range children[t] {
			visit(c)
		}
		order = append(order, "DROP TABLE "+t)
	}
	for _, t := range all {
		visit(t)
	}
	if err := d.runDDL(ctx, conn, order); err != nil {
		return fmt.Errorf("drop tables: %w", err)
	}
	return nil
}

func qualified(schema, name string) string {
	if schema == "" || schema == "public" {
		return quoteIdent(name)
	}
	return quoteIdent(schema) + "." + quoteIdent(name)
}

// queryStrings runs a query whose columns are all text and returns its rows.
func queryStrings(ctx context.Context, conn *sql.Conn, query string) ([][]string, error) {
	rows, err := conn.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	var out [][]string
	for rows.Next() {
		vals := make([]sql.NullString, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		r := make([]string, len(cols))
		for i, v := range vals {
			r[i] = v.String
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// dropViews drops every view. A view may be built on another, which can't
// be dropped first, and the catalog doesn't say which: views are dropped one
// at a time, in passes, keeping those Spanner refuses for the next pass,
// until none is left or a pass drops none.
func (d Dialect) dropViews(ctx context.Context, conn *sql.Conn) error {
	rows, err := queryStrings(ctx, conn, `SELECT table_schema, table_name FROM information_schema.views WHERE table_schema NOT IN `+systemSchemas)
	if err != nil {
		return fmt.Errorf("list views: %w", err)
	}
	left := make([]string, len(rows))
	for i, r := range rows {
		left[i] = "DROP VIEW " + qualified(r[0], r[1])
	}
	for len(left) > 0 {
		var next []string
		var last error
		for _, q := range left {
			if err := d.runDDL(ctx, conn, []string{q}); err != nil {
				next, last = append(next, q), err
			}
		}
		if len(next) == len(left) {
			return fmt.Errorf("drop views: %w", last)
		}
		left = next
	}
	return nil
}
