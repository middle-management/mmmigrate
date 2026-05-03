# Getting started

This page walks you through installing mmmigrate, creating your first migration, and shipping it to production.

## Install

### Homebrew

```bash
brew install middle-management/tap/mmmigrate
```

The formula lives at [middle-management/homebrew-tap](https://github.com/middle-management/homebrew-tap).

### Go install

mmmigrate ships as a single Go binary. Pick the build tag for your database driver — only one driver is compiled in per binary:

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

### Prebuilt binaries

Prebuilt binaries are also published on [GitHub Releases](https://github.com/middle-management/mmmigrate/releases).

Verify the install:

```bash
mmmigrate version
```

## Initialize a project

Create the migrations directory and an empty `current.sql`:

```bash
mmmigrate init
```

This produces:

```
migrations/
└── current.sql
```

`current.sql` is your live editing surface during development. It's where you write SQL until you're ready to lock it in as a numbered migration.

## Write your first migration

Edit `migrations/current.sql`:

```sql
CREATE TABLE IF NOT EXISTS users (
    id INTEGER PRIMARY KEY,
    name TEXT NOT NULL,
    email TEXT
);
```

!!! tip "current.sql must be idempotent"
    `current.sql` is re-run every time its checksum changes, so write SQL that is safe to run multiple times — `CREATE TABLE IF NOT EXISTS`, `DROP FUNCTION IF EXISTS ... CREATE FUNCTION`, etc.

## Apply during development

Point at your dev database and apply:

```bash
export DATABASE_URL="postgres://localhost/myapp_dev"
mmmigrate apply -current
```

`-current` tells mmmigrate to include `current.sql` after running any committed migrations. Without `-current`, only numbered migrations are applied — that's the production behavior.

Iterate freely: edit `current.sql`, re-run `mmmigrate apply -current`, repeat. mmmigrate detects checksum changes and re-applies.

For a tighter loop, use [`watch`](commands.md#watch) — it re-applies on every file save.

## Commit the migration

When you're happy with the schema change, commit it:

```bash
mmmigrate commit -description "create users table"
```

This:

1. Tests `current.sql` against your dev database (inside a transaction that gets rolled back, where supported).
2. Computes a checksum and chain hash.
3. Writes a numbered migration file: `migrations/001_create_users_table.sql`.
4. Empties `current.sql` for your next change.

!!! warning "MySQL needs a shadow database"
    MySQL/MariaDB cannot roll back DDL, so the dry-run can't safely test schema changes against your dev database. Use [`-shadow-url`](shadow-database.md) to verify against a disposable database instead.

## Apply in production

In production, run only the committed numbered migrations — never `-current`:

```bash
DATABASE_URL="postgres://prod/myapp" mmmigrate apply
```

Each migration runs in its own transaction (where the dialect supports it). Already-applied migrations are skipped automatically.

## What's next

- [The full workflow](workflow.md) — edit, apply, commit, revert.
- [Includes](includes.md) — share SQL between migrations.
- [Integrity](integrity.md) — how mmmigrate detects tampering.
- [Driver-specific notes](drivers/index.md) — PostgreSQL, SQLite, MySQL caveats.
