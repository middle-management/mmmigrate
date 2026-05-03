# Library API

mmmigrate is also a Go library. The root module is pure — it depends on `database/sql` and nothing else — so embedding it in an application doesn't pull in any database driver. You pick the driver you want and inject its `Dialect` at runtime.

## Modules

| Module | Path |
|--------|------|
| Engine + `Dialect` interface | `github.com/middle-management/mmmigrate` |
| Filesystem operations | `github.com/middle-management/mmmigrate/source` |
| PostgreSQL dialect | `github.com/middle-management/mmmigrate/driver/postgres` |
| SQLite dialect | `github.com/middle-management/mmmigrate/driver/sqlite` |
| MySQL dialect | `github.com/middle-management/mmmigrate/driver/mysql` |

## Quick start: run migrations on app startup

```go
package main

import (
    "context"
    "database/sql"
    "log"

    _ "github.com/jackc/pgx/v5/stdlib"

    "github.com/middle-management/mmmigrate"
    "github.com/middle-management/mmmigrate/driver/postgres"
)

func main() {
    db, err := sql.Open("pgx", "postgres://localhost/myapp")
    if err != nil {
        log.Fatal(err)
    }
    defer db.Close()

    ctx := context.Background()
    if err := mmmigrate.RunMigrations(ctx, db, postgres.Dialect{}, "migrations", false); err != nil {
        log.Fatalf("migrate: %v", err)
    }
}
```

The fourth argument is `applyCurrent` — pass `true` to also apply `current.sql` (development), `false` for production-only (committed migrations).

## Public functions

| Function | Purpose |
|----------|---------|
| `mmmigrate.RunMigrations(ctx, db, dialect, dir, applyCurrent)` | Equivalent to the CLI's `apply` command |
| `mmmigrate.Status(ctx, db, dialect, dir)` | Returns applied/pending state for each migration |
| `mmmigrate.DryRun(ctx, db, dialect, dir, applyCurrent)` | Returns the SQL that would run, without executing |
| `mmmigrate.TestCurrentMigration(ctx, db, dir)` | Runs `current.sql` in a rolled-back transaction (used by `commit`) |
| `mmmigrate.VerifyAgainstShadow(ctx, shadowDB, dialect, dir)` | Resets and replays the chain on a shadow DB |

For lower-level control, construct a `Migrator` directly:

```go
m := mmmigrate.NewMigrator(db, postgres.Dialect{}, "migrations", false)
migrations, err := source.LoadMigrations("migrations")
if err != nil { /* ... */ }
if err := m.Run(ctx, migrations); err != nil { /* ... */ }
```

## Choosing a dialect

```go
import "github.com/middle-management/mmmigrate/driver/postgres" // PostgreSQL
import "github.com/middle-management/mmmigrate/driver/sqlite"   // SQLite
import "github.com/middle-management/mmmigrate/driver/mysql"    // MySQL

// Then pass an instance to RunMigrations:
postgres.Dialect{}
sqlite.Dialect{}
mysql.Dialect{}
```

Each driver module imports the corresponding `database/sql` adapter (`pgx`, `modernc.org/sqlite`, `go-sql-driver/mysql`). Importing the dialect package transitively pulls in its driver — which is what you want when embedding mmmigrate in an application.

## Source-level operations

The `source` package handles loading, committing, and reverting migration files independently of the database. It's useful in tooling that wraps mmmigrate (custom CLIs, IDE integrations, CI scripts):

```go
import "github.com/middle-management/mmmigrate/source"

migrations, err := source.LoadMigrations("migrations")
// migrations is []*source.Migration with Version, Description, Body, Checksum, Chain.
```

## Testing

For tests, the `migratetest` package provides a shared harness used by the project's own driver test suites. Pair it with `:memory:` SQLite for hermetic, fast tests:

```go
import (
    "database/sql"
    _ "modernc.org/sqlite"

    "github.com/middle-management/mmmigrate"
    "github.com/middle-management/mmmigrate/driver/sqlite"
)

db, _ := sql.Open("sqlite", ":memory:")
mmmigrate.RunMigrations(ctx, db, sqlite.Dialect{}, "testdata/migrations", true)
```
