//go:build js && wasm

// Package jsdb is a database/sql driver adapter that bridges Go to a JS-side
// database exposing the pglite-shaped interface:
//
//	{
//	  exec(sql):           Promise<unknown>           // multi-statement OK, no params
//	  query(sql, params):  Promise<{ rows, fields, affectedRows }>
//	}
//
// where rows is an array of plain objects keyed by column name and fields
// is an array of { name, ... }. Both pglite (postgres in WASM) and a thin
// adapter over @sqlite.org/sqlite-wasm satisfy this contract.
//
// Use NewConnector + sql.OpenDB to wire it up; do not use sql.Open / a DSN.
// The js.Value held here is the "db" object the JS host hands us.
package jsdb

import (
	"context"
	"database/sql/driver"
	"syscall/js"
)

// NewConnector returns a database/sql connector backed by the supplied JS db.
func NewConnector(db js.Value) driver.Connector {
	return &connector{db: db}
}

type connector struct{ db js.Value }

func (c *connector) Connect(_ context.Context) (driver.Conn, error) {
	return &conn{db: c.db}, nil
}

func (c *connector) Driver() driver.Driver {
	return &jsDriver{db: c.db}
}

type jsDriver struct{ db js.Value }

// Open is required by driver.Driver but is not how callers should connect;
// use NewConnector + sql.OpenDB. The DSN is ignored.
func (d *jsDriver) Open(_ string) (driver.Conn, error) {
	return &conn{db: d.db}, nil
}
