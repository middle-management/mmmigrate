---
name: mmmigrate
description: Use when a project uses mmmigrate for database migrations, when there is a migrations/ directory with current.sql, numbered .sql files, or @include directives, when user asks to add/modify database tables, create migrations, apply schema changes, or validate migration integrity
---

# mmmigrate

File-based forward-only SQL migration tool for PostgreSQL, SQLite, MySQL, and Spanner: `current.sql` dev workflow, `@include` for shared SQL, merkle chain integrity. No down migrations — roll forward only.

## Setup

```bash
mkdir -p migrations
touch migrations/current.sql   # your working file
```

Build the CLI with a driver tag: `cd cmd/mmmigrate && go build -tags sqlite .`, `go build -tags postgres .`, `go build -tags mysql .`, or `go build -tags spanner .`

## Workflow

```
edit current.sql → apply -current (dev, iterate) → commit → apply (prod)
                                                    ↕
                                                  revert (files only)
```

1. Write **idempotent** SQL in `migrations/current.sql` (use `-- @include path` for shared files). Idempotent means safe to re-run: use `CREATE TABLE IF NOT EXISTS`, `DROP FUNCTION IF EXISTS ... CREATE FUNCTION`, etc.
2. Dev: `mmmigrate apply -current` — runs numbered migrations + current.sql. Re-running after edits reapplies if the checksum changed, so current.sql **must** be idempotent
3. Ready: `mmmigrate commit -description "..."` — tests SQL against DB, creates `NNN_name.sql` with checksum/chain, clears current.sql
4. Prod: `mmmigrate apply` — only committed numbered migrations, each in its own transaction
5. Mistake: `mmmigrate revert` — **file operation only**: moves last committed migration back to current.sql, restoring `@include` directives. Does NOT undo applied SQL on the database. To fix an applied migration, write corrective SQL in current.sql and commit a new migration.

## Commands

| Command | Needs DB | Purpose |
|---------|----------|---------|
| `init` | no | Create migrations directory and empty current.sql |
| `apply [-current] [-dry-run]` | yes | Run pending migrations. `-current` includes current.sql, `-dry-run` shows what would run |
| `commit -description "..." [-shadow-url URL] [-skip-verify]` | yes* | Test and commit current.sql as numbered migration |
| `revert` | no | Uncommit last migration back to current.sql (files only) |
| `status` | yes | Show which migrations are applied/pending |
| `render` | no | Print current.sql with includes expanded (useful for piping to psql) |
| `check` | no | Verify current.sql has no uncommitted changes (CI gate) |
| `validate` | no | Verify checksums and merkle chain of all migrations |
| `watch [-debounce DURATION]` | yes | Watch current.sql + includes and re-apply on change (dev loop) |
| `version` | no | Print version |

*`commit` skips the DB test when `-skip-verify` is set. `-shadow-url` (or `SHADOW_DATABASE_URL`) additionally replays the full chain on a disposable database — required for MySQL and Spanner DDL. The shadow reset empties the whole database; if migrations depend on objects they don't create (another app's schema), pass them with `-shadow-baseline FILE` (repeatable, or `MMMIGRATE_SHADOW_BASELINE`), loaded after the reset and before the replay.

All commands accept `-migrations DIR` (default: `migrations`). DB commands accept `-database-url URL` (default: `DATABASE_URL` env). `apply`, `status`, `commit` and `watch` also accept `-namespace NAME` (default: `MMMIGRATE_NAMESPACE` env, then `default`) for several apps sharing one database.

## Committed Migration Format

```sql
-- Migration: add users table
-- Created: 2026-03-24T12:00:00Z
-- Checksum: a1b2c3...
-- Chain: d4e5f6...
--
-- IMPORTANT: Do not modify this file after commit.
--
CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT);
```

Versions are auto-incrementing integers, zero-padded to 3 digits (`001_`, `002_`, ...).

## Include System

Shared SQL (functions, views) lives in subdirectories:

```sql
-- current.sql
CREATE TABLE events (id INTEGER PRIMARY KEY, name TEXT);
-- @include functions/set_geom.sql
-- @include views/upcoming_events.sql
```

On commit, includes expand inline with `BEGIN/END INCLUDE` markers. On revert, markers restore to `@include` directives. Paths must stay within the migrations directory.

## Drivers

Each driver is a separate Go module. As a library:

```go
import (
    "github.com/middle-management/mmmigrate"
    "github.com/middle-management/mmmigrate/driver/postgres" // or driver/sqlite, driver/mysql, driver/spanner
)

// Use the dialect explicitly:
// mmmigrate.RunMigrations(ctx, db, postgres.Dialect{}, os.DirFS("migrations"), false)
```

## Integrity

- **Checksum** — sha256 of SQL body, detects individual file tampering
- **Chain** — sha256(prev_chain + checksum), merkle chain where any earlier change invalidates all subsequent migrations

`mmmigrate validate` verifies both. Use `mmmigrate check && mmmigrate validate` in CI.

## Tracking Tables

- **PostgreSQL**: `mmmigrate.applied`, `mmmigrate.current` (own schema)
- **SQLite/MySQL**: `mmmigrate_applied`, `mmmigrate_current`
- **Spanner**: `mmmigrate_applied`, `mmmigrate_current`, `mmmigrate_progress`, `mmmigrate_lock`

Both tables have a `namespace` column (`default` unless `-namespace` / `MMMIGRATE_NAMESPACE` is set); `namespace` has no default, so include it in manual INSERTs. Library: `mmmigrate.WithNamespace("name")`. Each app sharing a database needs its own namespace and its own shadow database; use the same namespace for every command.

## MySQL Limitations

MySQL/MariaDB DDL (`CREATE TABLE`, `ALTER TABLE`) causes an **implicit commit** and cannot be rolled back. This means:

- **`commit` dry-run** cannot roll back DDL. Use `-shadow-url` to test against a disposable database instead.
- **Partial failures** leave completed DDL in place. Keep migrations small — ideally one DDL statement per migration.

PostgreSQL and SQLite do not have this limitation.

## Spanner Limitations

Spanner (PostgreSQL dialect) can't run DDL in a transaction, nor DDL and DML in the same one. This means:

- **Non-transactional DDL**: each migration runs outside a transaction, with consecutive DDL statements grouped into batches. It is recorded in `mmmigrate_applied` only once every statement has succeeded.
- **Interrupted migrations** resume from `mmmigrate_progress`; the group that was under way re-runs in recovery mode, with idempotent forms (`IF NOT EXISTS`, `IF EXISTS`) where Spanner has them. Statements without one (`ALTER COLUMN`, `ADD CONSTRAINT`, `DROP COLUMN`) fail if they already took effect, so keep them in a migration of their own.
- **Locking** is a lease row per namespace in `mmmigrate_lock`; a run that dies leaves its lease to expire (60 seconds by default).
- **`commit` needs `-shadow-url`** (or `-skip-verify`), since there is no transaction to roll back. The shadow reset drops every user object, tracking tables included.

## Common Mistakes

| Mistake | Fix |
|---------|-----|
| Editing a committed migration | Never modify committed files — checksums and chain will fail. Use `revert` then re-commit |
| Forgetting `-current` in dev | Without it, `apply` only runs numbered migrations, not current.sql |
| Expecting `revert` to undo DB changes | `revert` is file-only. Write corrective SQL and commit a new migration |
| Committing without DB | `commit` tests SQL against a real database — provide DATABASE_URL |
| Non-idempotent current.sql | current.sql is re-run when it changes — use `IF NOT EXISTS`, `DROP ... CREATE`, etc. |
| Duplicate version numbers | Two files with the same `NNN_` prefix will error on load |
| `apply` on a database graphile-migrate already migrated | It replays the whole history. Adopt it first with step 6 of `docs/migrating-from-graphile.md` (matches files by `--! Hash:`, refuses when unsure); never hand-insert `applied` rows by number |
