# mmmigrate

A forward-only SQL migration tool for PostgreSQL, SQLite, and MySQL, inspired by [Graphile Migrate](https://github.com/graphile/migrate).

Migrations are plain SQL files. You edit `current.sql` during development, commit it as a numbered migration when ready, and apply to production. Shared SQL (functions, views) can be reused across migrations via `@include` directives. A merkle chain ensures no committed migration is ever tampered with.

## Install

```bash
# From source (pick your driver)
go install -tags sqlite   github.com/middle-management/mmmigrate/cmd/mmmigrate@latest
go install -tags postgres  github.com/middle-management/mmmigrate/cmd/mmmigrate@latest
go install -tags mysql     github.com/middle-management/mmmigrate/cmd/mmmigrate@latest

# Or download a binary from GitHub Releases
```

## Quick start

```bash
mkdir -p migrations
cat > migrations/current.sql <<'SQL'
CREATE TABLE users (
    id INTEGER PRIMARY KEY,
    name TEXT NOT NULL,
    email TEXT
);
SQL

# Apply in development (includes current.sql)
export DATABASE_URL="postgres://localhost/myapp_dev"
mmmigrate apply -current

# Commit when ready
mmmigrate commit -description "create users table"

# Apply in production (committed migrations only)
DATABASE_URL="postgres://prod/myapp" mmmigrate apply
```

## Commands

| Command | Needs DB | Description |
|---------|----------|-------------|
| `init` | no | Create migrations directory and empty current.sql |
| `apply [-current] [-dry-run]` | yes | Run pending migrations. `-current` includes current.sql |
| `commit -description "..."` | yes | Test and commit current.sql as a numbered migration |
| `revert` | no | Uncommit last migration back to current.sql |
| `status` | yes | Show which migrations are applied/pending |
| `render` | no | Print current.sql with includes expanded (pipe to psql) |
| `check` | no | Verify current.sql has no uncommitted changes |
| `validate` | no | Verify checksums and merkle chain integrity |
| `version` | no | Print version |

All commands accept `-migrations DIR` (default `migrations`) and `-database-url URL` (default `DATABASE_URL` env).

## Includes

Shared SQL lives in subdirectories and is referenced with `@include`:

```sql
-- migrations/current.sql
CREATE TABLE events (id SERIAL PRIMARY KEY, name TEXT);
-- @include functions/notify_event.sql
```

On commit, includes are expanded inline. On revert, they are restored to `@include` directives. Paths are restricted to the migrations directory.

## Integrity

Each committed migration has a content checksum and a chain hash linking it to all previous migrations. `mmmigrate validate` verifies both — if any migration is modified, the chain breaks.

```
-- Migration: create users table
-- Checksum: a1b2c3...
-- Chain: d4e5f6...
```

## Concurrency

PostgreSQL uses an advisory lock, MySQL uses a named lock (`GET_LOCK`), and SQLite uses its native file-level locking — all safe for multi-pod deployments.

## MySQL limitations

MySQL and MariaDB DDL (`CREATE TABLE`, `ALTER TABLE`, etc.) causes an implicit commit and **cannot be rolled back** within a transaction. This affects mmmigrate in two ways:

- **`commit` dry-run**: `TestCurrentMigration` cannot roll back DDL changes on MySQL. Use `-shadow-url` to verify migrations against a disposable database instead.
- **Partial failures**: if a migration with multiple DDL statements fails partway through, the completed statements cannot be undone. Keep migrations small — ideally one DDL statement per migration.

PostgreSQL and SQLite both support transactional DDL and do not have these limitations.

## As a library

The root module is a pure library with no driver dependencies:

```go
import (
    "github.com/middle-management/mmmigrate"
    "github.com/middle-management/mmmigrate/driver/postgres" // or driver/sqlite, driver/mysql
)

// mmmigrate.RunMigrations(ctx, db, postgres.Dialect{}, "migrations", false)
```

## Differences from Graphile Migrate

mmmigrate borrows the `current.sql` workflow from [Graphile Migrate](https://github.com/graphile/migrate) but differs in several ways:

| | Graphile Migrate | mmmigrate |
|---|---|---|
| **Language** | Node.js | Go (single binary, no runtime) |
| **Databases** | PostgreSQL only | PostgreSQL, SQLite, and MySQL via pluggable drivers |
| **Integrity** | SHA-1 hash chain (`--! Hash:`) | SHA-256 checksums + merkle chain (`-- Chain:`) |
| **Includes** | `--! include` from a fixtures folder | `-- @include` from migrations subdirectories, restored on revert |
| **Shadow DB** | Required, auto-created via root DB connection | Optional (`-shadow-url`), user-managed |
| **Concurrency** | Advisory lock | Advisory lock (PostgreSQL), named lock (MySQL), file lock (SQLite) |
| **current.sql** | Must be idempotent; re-run on every file save (watch mode) | Must be idempotent; re-run when checksum changes |
| **Watch mode** | Yes (auto-applies on file change) | No (explicit `apply -current`) |
| **Placeholders** | `:PLACEHOLDER_NAME` substitution in SQL | Not supported |
| **Hooks** | beforeReset, afterReset, beforeAll, afterAll, etc. | Not supported |
| **Down migrations** | Not supported (forward-only) | Not supported (forward-only) |
| **Usable as library** | Undocumented, not a public API | Yes — `mmmigrate` and `source` packages with `database/sql` |

## License

MIT
