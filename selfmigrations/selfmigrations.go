// Package selfmigrations holds the migrations mmmigrate applies to its own
// tracking tables, one directory per SQL flavour. They are ordinary mmmigrate
// migrations, recorded under mmmigrate.SelfNamespace, so a database created by
// an older version is upgraded on its next run.
//
// Only the applied table is created outside of these migrations (see
// Dialect.CreateMigrationsTable), because it is needed to record them. Any
// later change to the tracking tables, including the applied table, goes here.
//
// To add one, write the change for every flavour in its current.sql and commit
// it with the CLI, e.g.
//
//	mmmigrate commit -migrations selfmigrations/postgres -description "..." -skip-verify
package selfmigrations

import (
	"embed"
	"io/fs"
)

//go:embed postgres/[0-9]*.sql sqlite/[0-9]*.sql mysql/[0-9]*.sql
var files embed.FS

var (
	// Postgres is used by the postgres and pglite dialects.
	Postgres = sub("postgres")
	// SQLite is used by the sqlite and sqlitejs dialects.
	SQLite = sub("sqlite")
	// MySQL is used by the mysql dialect.
	MySQL = sub("mysql")
)

func sub(dir string) fs.FS {
	fsys, err := fs.Sub(files, dir)
	if err != nil {
		panic(err)
	}
	return fsys
}
