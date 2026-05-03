# mmmigrate

A forward-only SQL migration tool for PostgreSQL, SQLite, and MySQL, inspired by [Graphile Migrate](https://github.com/graphile/migrate).

📚 **Documentation:** <https://middle-management.github.io/mmmigrate/>

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

## Learn more

- **[Workflow](https://middle-management.github.io/mmmigrate/workflow/)** — the `current.sql` editing loop.
- **[Includes](https://middle-management.github.io/mmmigrate/includes/)** — share SQL across migrations with `@include`.
- **[Integrity](https://middle-management.github.io/mmmigrate/integrity/)** — checksums and merkle chain.
- **[Shadow database](https://middle-management.github.io/mmmigrate/shadow-database/)** — verify migrations on a disposable DB (required for MySQL).
- **[Drivers](https://middle-management.github.io/mmmigrate/drivers/)** — PostgreSQL, SQLite, MySQL specifics.
- **[Library API](https://middle-management.github.io/mmmigrate/library/)** — embed mmmigrate in a Go program.
- **[Migrating from Graphile](https://middle-management.github.io/mmmigrate/migrating-from-graphile/)** — porting notes and feature comparison.

## License

MIT
