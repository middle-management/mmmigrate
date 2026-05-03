# Drivers

mmmigrate supports PostgreSQL, SQLite, and MySQL through pluggable drivers. Each driver is a separate Go module that implements the `mmmigrate.Dialect` interface (locking, tracking-table SQL, parameter style, reset SQL).

## Choosing a driver

The CLI is built with **Go build tags** so each binary contains exactly one driver — keeping binary size small and avoiding cross-driver dependency surface:

=== "SQLite"

    ```bash
    go install -tags sqlite github.com/middle-management/mmmigrate/cmd/mmmigrate@latest
    ```

=== "PostgreSQL"

    ```bash
    go install -tags postgres github.com/middle-management/mmmigrate/cmd/mmmigrate@latest
    ```

=== "MySQL"

    ```bash
    go install -tags mysql github.com/middle-management/mmmigrate/cmd/mmmigrate@latest
    ```

If you want a single binary that supports more than one driver, build it yourself — the build tags are not mutually exclusive at compile time, just selected one-at-a-time in the published install commands.

## Per-driver pages

| Page | Highlights |
|------|------------|
| [PostgreSQL](postgres.md) | Advisory locks, schema-based tracking tables, transactional DDL |
| [SQLite](sqlite.md) | File-level locking, transactional DDL, ideal for tests |
| [MySQL](mysql.md) | Named locks, **non-transactional DDL**, shadow database required |

## Module layout

| Module | Path | Purpose |
|--------|------|---------|
| Library | `github.com/middle-management/mmmigrate` | Engine, `Dialect` interface, no driver deps |
| Source | `github.com/middle-management/mmmigrate/source` | Filesystem ops (load, commit, revert) |
| CLI | `github.com/middle-management/mmmigrate/cmd/mmmigrate` | Build-tag-selected binary |
| PostgreSQL driver | `github.com/middle-management/mmmigrate/driver/postgres` | `pgx` + `Dialect` |
| SQLite driver | `github.com/middle-management/mmmigrate/driver/sqlite` | `modernc.org/sqlite` + `Dialect` |
| MySQL driver | `github.com/middle-management/mmmigrate/driver/mysql` | `go-sql-driver/mysql` + `Dialect` |

The library module imports no database drivers — that's why it's safe to depend on from any project. Driver modules pull in their respective `database/sql` adapters.

See [Library API](../library.md) for embedding mmmigrate in a Go program.
