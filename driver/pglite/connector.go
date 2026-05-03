//go:build js && wasm

package pglite

import (
	"context"
	"database/sql/driver"
	"syscall/js"
)

// NewConnector returns a database/sql connector backed by the supplied
// pglite JS instance. Pass the result to sql.OpenDB.
//
// The connector wraps a single JS instance. pglite is itself single-threaded,
// so all "connections" returned point at the same backing database.
func NewConnector(db js.Value) driver.Connector {
	return &connector{db: db}
}

type connector struct{ db js.Value }

func (c *connector) Connect(_ context.Context) (driver.Conn, error) {
	return &conn{db: c.db}, nil
}

func (c *connector) Driver() driver.Driver {
	return &pgliteDriver{db: c.db}
}

type pgliteDriver struct{ db js.Value }

// Open is required by driver.Driver but is not how callers should connect;
// use NewConnector + sql.OpenDB. The DSN is ignored.
func (d *pgliteDriver) Open(_ string) (driver.Conn, error) {
	return &conn{db: d.db}, nil
}
