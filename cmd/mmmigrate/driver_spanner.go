//go:build spanner

package main

import (
	// Spanner's PostgreSQL dialect through PGAdapter: a postgres:// URL to
	// PGAdapter, which names the Spanner database.
	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/middle-management/mmmigrate"
	"github.com/middle-management/mmmigrate/driver/spanner"
)

var dialect mmmigrate.Dialect = spanner.Dialect{}
