//go:build js && wasm

package pglite

import (
	"context"
	"database/sql/driver"
	"errors"
	"syscall/js"
)

type conn struct{ db js.Value }

func (c *conn) Prepare(query string) (driver.Stmt, error) {
	return &stmt{db: c.db, query: query}, nil
}

func (c *conn) Close() error { return nil }

// Begin issues a BEGIN against the underlying pglite instance. mmmigrate uses
// transactions to apply each migration atomically; pglite supports raw
// BEGIN/COMMIT/ROLLBACK statements on its single connection.
func (c *conn) Begin() (driver.Tx, error) {
	if _, err := await(c.db.Call("exec", "BEGIN")); err != nil {
		return nil, err
	}
	return &tx{db: c.db}, nil
}

// ExecContext runs a statement, optionally with parameters. Without
// parameters we use pglite's .exec() which supports multi-statement SQL —
// migration files frequently contain several statements separated by ";".
// With parameters we fall back to .query() which is single-statement only.
func (c *conn) ExecContext(_ context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if len(args) == 0 {
		if _, err := await(c.db.Call("exec", query)); err != nil {
			return nil, err
		}
		return driver.RowsAffected(0), nil
	}
	res, err := await(c.db.Call("query", query, argsToJSArray(args)))
	if err != nil {
		return nil, err
	}
	var affected int64
	if v := res.Get("affectedRows"); !v.IsUndefined() && !v.IsNull() {
		affected = int64(v.Float())
	}
	return driver.RowsAffected(affected), nil
}

func (c *conn) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	res, err := await(c.db.Call("query", query, argsToJSArray(args)))
	if err != nil {
		return nil, err
	}
	return newRows(res), nil
}

// Compile-time interface checks.
var (
	_ driver.Conn           = (*conn)(nil)
	_ driver.ExecerContext  = (*conn)(nil)
	_ driver.QueryerContext = (*conn)(nil)
)

type tx struct{ db js.Value }

func (t *tx) Commit() error {
	_, err := await(t.db.Call("exec", "COMMIT"))
	return err
}

func (t *tx) Rollback() error {
	_, err := await(t.db.Call("exec", "ROLLBACK"))
	return err
}

// stmt is a minimal Stmt implementation. Callers normally hit ExecContext /
// QueryContext on the conn directly; Prepare exists for completeness.
type stmt struct {
	db    js.Value
	query string
}

func (s *stmt) Close() error  { return nil }
func (s *stmt) NumInput() int { return -1 }

func (s *stmt) Exec(args []driver.Value) (driver.Result, error) {
	return nil, errors.New("pglite: use ExecContext")
}

func (s *stmt) Query(args []driver.Value) (driver.Rows, error) {
	return nil, errors.New("pglite: use QueryContext")
}

func (s *stmt) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	return (&conn{db: s.db}).ExecContext(ctx, s.query, args)
}

func (s *stmt) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	return (&conn{db: s.db}).QueryContext(ctx, s.query, args)
}

var (
	_ driver.Stmt             = (*stmt)(nil)
	_ driver.StmtExecContext  = (*stmt)(nil)
	_ driver.StmtQueryContext = (*stmt)(nil)
)
