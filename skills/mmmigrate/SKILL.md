---
name: mmmigrate
description: Use when a project uses mmmigrate for database migrations, when there is a migrations/ directory with current.sql, numbered .sql files, or @include directives, when user asks to add/modify database tables, create migrations, apply schema changes, or validate migration integrity
---

# mmmigrate

File-based SQL migration tool: `current.sql` dev workflow, `@include` for shared SQL, merkle chain integrity.

## Workflow

```
edit current.sql → apply -current (dev) → commit → apply (prod)
                                          ↕
                                        revert
```

1. Write SQL in `current.sql` (use `-- @include path` for shared files)
2. Dev: `mmmigrate apply -current` — runs numbered + current.sql
3. Ready: `mmmigrate commit -description "..."` — tests SQL, creates `NNN_name.sql` with checksum/chain, clears current.sql
4. Prod: `mmmigrate apply` — only committed migrations
5. Mistake: `mmmigrate revert` — uncommits last migration back to current.sql, restoring `@include` directives

## Commands

| Command | Needs DB | Purpose |
|---------|----------|---------|
| `apply [-current]` | yes | Run pending migrations. `-current` also applies current.sql |
| `commit -description "..."` | yes | Test and commit current.sql as numbered migration |
| `revert` | no | Uncommit last migration back to current.sql |
| `check` | no | Verify current.sql has no uncommitted changes (CI gate) |
| `validate` | no | Verify checksums and merkle chain of all migrations |

All commands accept `-migrations DIR` (default: `migrations`) and DB commands accept `-database-url URL` (default: `DATABASE_URL` env).

## Include System

Shared SQL (functions, views) lives in subdirectories and is referenced via `@include`:

```sql
-- current.sql
CREATE TABLE events (id INTEGER PRIMARY KEY, name TEXT);
-- @include functions/set_geom.sql
-- @include views/upcoming_events.sql
```

On commit, includes are expanded inline with `BEGIN/END INCLUDE` markers. On revert, markers are restored to `@include` directives. Include paths must stay within the migrations directory (path traversal is rejected).

## Build Tags and Drivers

The CLI needs a driver build tag:

```bash
go build -tags sqlite  .   # links SQLite driver
go build -tags postgres .  # links PostgreSQL driver
```

As a library, import only the driver you need:

```go
import (
    "github.com/middle-management/mmmigrate/migrate"
    "github.com/middle-management/mmmigrate/source"
    _ "github.com/middle-management/mmmigrate/driver/sqlite"   // or driver/postgres
)
```

Each driver is a separate Go module — consumers only pull in the dependencies they need.

## Integrity

Each committed migration has:
- **Checksum** — sha256 of the SQL body, detects tampering of individual files
- **Chain** — sha256(previous_chain + checksum), a merkle chain where modifying any earlier migration invalidates all subsequent ones

`mmmigrate validate` verifies both in a single pass.

## Tracking Tables

- **PostgreSQL**: `mmmigrate.applied`, `mmmigrate.current` (own schema)
- **SQLite**: `mmmigrate_applied`, `mmmigrate_current`

## Common Mistakes

| Mistake | Fix |
|---------|-----|
| Editing a committed migration | Never modify committed files — checksums and chain will fail. Use `revert` instead |
| Forgetting `-current` in dev | Without it, `apply` only runs numbered migrations, not current.sql |
| Committing without DB | `commit` tests the SQL against a real database first — provide DATABASE_URL |
| Duplicate version numbers | Two files with the same `NNN_` prefix will error on load |
