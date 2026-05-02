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
| `commit -description "..." [-shadow-url URL] [-skip-verify]` | yes* | Test and commit current.sql as a numbered migration |
| `revert` | no | Uncommit last migration back to current.sql |
| `status` | yes | Show which migrations are applied/pending |
| `render` | no | Print current.sql with includes expanded (pipe to psql) |
| `check` | no | Verify current.sql has no uncommitted changes |
| `validate` | no | Verify checksums and merkle chain integrity |
| `watch [-debounce DURATION]` | yes | Watch current.sql + includes and re-apply on change |
| `version` | no | Print version |

*`commit` does not need a database connection when `-skip-verify` is used.

All commands accept `-migrations DIR` (default `migrations`). Database commands accept `-database-url URL` (default `DATABASE_URL` env).

## Includes

Shared SQL lives in subdirectories and is referenced with `@include`:

```sql
-- migrations/current.sql
CREATE TABLE events (id SERIAL PRIMARY KEY, name TEXT);
-- @include functions/notify_event.sql
```

On commit, includes are expanded inline. On revert, they are restored to `@include` directives. Paths are restricted to the migrations directory.

## Watch mode

`mmmigrate watch` re-applies `current.sql` (and any `@include`d files) whenever they change on disk. It does an initial apply on startup, then watches the migrations directory and any subdirectories containing includes. New `@include` directives are picked up automatically after the next save.

```bash
mmmigrate watch                          # default debounce 200ms
mmmigrate watch -debounce 500ms          # coalesce bursts of editor events
```

This is the equivalent of running `apply -current` on every save. Stop with Ctrl-C.

## Integrity

Each committed migration has a content checksum and a chain hash linking it to all previous migrations. `mmmigrate validate` verifies both — if any migration is modified, the chain breaks.

```
-- Migration: create users table
-- Checksum: a1b2c3...
-- Chain: d4e5f6...
```

## Concurrency

PostgreSQL uses an advisory lock, MySQL uses a named lock (`GET_LOCK`), and SQLite uses its native file-level locking — all safe for multi-pod deployments.

## Shadow database

When you run `mmmigrate commit`, the tool tests `current.sql` against your development database inside a transaction and rolls it back. This works well for PostgreSQL and SQLite, which support transactional DDL. On MySQL, however, DDL statements (`CREATE TABLE`, `ALTER TABLE`, etc.) cause an implicit commit and **cannot be rolled back**, leaving your dev database in a dirty state.

The shadow database solves this: it's a separate, disposable database that mmmigrate resets, then replays every committed migration plus `current.sql` from scratch. This verifies the entire migration chain works on a clean database — not just the latest migration in isolation.

### When to use it

- **MySQL/MariaDB** — essentially required, since the normal commit dry-run can't roll back DDL.
- **Any database** — useful as an extra safety net before committing, especially in CI. It catches problems like migrations that depend on manual schema changes or ordering issues that only surface on a fresh database.

### Setup

Create a dedicated database for shadow use. It will be **fully wiped** on every run — never point it at a database you care about.

```bash
# PostgreSQL
createdb myapp_shadow

# MySQL
mysql -e "CREATE DATABASE myapp_shadow"

# SQLite — just use a temp file or :memory:
```

### Usage

Pass `-shadow-url` to `commit`, or set the `SHADOW_DATABASE_URL` environment variable:

```bash
# Via flag
mmmigrate commit -description "add events table" \
  -shadow-url "postgres://localhost/myapp_shadow"

# Via environment variable
export SHADOW_DATABASE_URL="postgres://localhost/myapp_shadow"
mmmigrate commit -description "add events table"
```

The shadow verification runs after the normal commit test passes. If shadow replay fails, the commit is aborted and no migration file is written.

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
| **Watch mode** | Yes (auto-applies on file change) | Yes (`mmmigrate watch`) or explicit `apply -current` |
| **Placeholders** | `:PLACEHOLDER_NAME` substitution in SQL | Not supported |
| **Hooks** | beforeReset, afterReset, beforeAll, afterAll, etc. | Not supported |
| **Down migrations** | Not supported (forward-only) | Not supported (forward-only) |
| **Usable as library** | Undocumented, not a public API | Yes — `mmmigrate` and `source` packages with `database/sql` |

## License

MIT
