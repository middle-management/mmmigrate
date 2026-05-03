# mmmigrate

A forward-only SQL migration tool for **PostgreSQL**, **SQLite**, and **MySQL**, inspired by [Graphile Migrate](https://github.com/graphile/migrate).

Migrations are plain SQL files. You edit `current.sql` during development, commit it as a numbered migration when ready, and apply to production. Shared SQL (functions, views) can be reused across migrations via `@include` directives. A merkle chain ensures no committed migration is ever tampered with.

## Install

=== "Homebrew"

    ```bash
    brew install middle-management/tap/mmmigrate
    ```

    See [middle-management/homebrew-tap](https://github.com/middle-management/homebrew-tap) for the formula.

=== "Go install (SQLite)"

    ```bash
    go install -tags sqlite github.com/middle-management/mmmigrate/cmd/mmmigrate@latest
    ```

=== "Go install (PostgreSQL)"

    ```bash
    go install -tags postgres github.com/middle-management/mmmigrate/cmd/mmmigrate@latest
    ```

=== "Go install (MySQL)"

    ```bash
    go install -tags mysql github.com/middle-management/mmmigrate/cmd/mmmigrate@latest
    ```

Or download a binary from [GitHub Releases](https://github.com/middle-management/mmmigrate/releases).

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

## What's in this site

<div class="grid cards" markdown>

-   **[Getting started](getting-started.md)** — install, set up your first migration, and run the dev → commit → prod loop.
-   **[Workflow](workflow.md)** — the `current.sql` editing loop, idempotency, and `watch` mode.
-   **[Commands](commands.md)** — full command reference with flags.
-   **[Includes](includes.md)** — reuse shared SQL with `@include` directives.
-   **[Integrity](integrity.md)** — checksums, merkle chain, and `validate`.
-   **[Shadow database](shadow-database.md)** — verify migrations on a disposable DB before commit.
-   **[Drivers](drivers/index.md)** — PostgreSQL, SQLite, MySQL specifics.
-   **[Library API](library.md)** — embed mmmigrate in a Go program.
-   **[Migrating from Graphile](migrating-from-graphile.md)** — porting notes and feature comparison.
-   **[FAQ](faq.md)** — common mistakes and troubleshooting.

</div>
