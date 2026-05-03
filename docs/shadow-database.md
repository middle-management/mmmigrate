# Shadow database

When you run `mmmigrate commit`, the tool tests `current.sql` against your dev database inside a transaction and rolls it back. This works well for PostgreSQL and SQLite, which support transactional DDL. On MySQL, however, DDL statements (`CREATE TABLE`, `ALTER TABLE`, etc.) cause an implicit commit and **cannot be rolled back**, leaving your dev database in a dirty state.

The shadow database solves this: it's a separate, disposable database that mmmigrate resets, then replays every committed migration plus `current.sql` from scratch. This verifies the entire migration chain works on a clean database — not just the latest migration in isolation.

## When to use it

- **MySQL/MariaDB** — essentially required, since the normal commit dry-run can't roll back DDL.
- **Any database** — useful as an extra safety net before committing, especially in CI. It catches problems like migrations that depend on manual schema changes or ordering issues that only surface on a fresh database.

## Setup

Create a dedicated database for shadow use. It will be **fully wiped** on every run — never point it at a database you care about.

=== "PostgreSQL"

    ```bash
    createdb myapp_shadow
    ```

=== "MySQL"

    ```bash
    mysql -e "CREATE DATABASE myapp_shadow"
    ```

=== "SQLite"

    Just use a temp file or `:memory:`.

!!! danger "The shadow database is wiped on every run"
    `mmmigrate commit -shadow-url ...` resets the entire shadow database before replaying the migration chain. Never aim it at a database that contains data you want to keep.

## Usage

Pass `-shadow-url` to `commit`, or set the `SHADOW_DATABASE_URL` environment variable:

=== "Flag"

    ```bash
    mmmigrate commit -description "add events table" \
      -shadow-url "postgres://localhost/myapp_shadow"
    ```

=== "Environment variable"

    ```bash
    export SHADOW_DATABASE_URL="postgres://localhost/myapp_shadow"
    mmmigrate commit -description "add events table"
    ```

The shadow verification runs after the normal commit dry-run. If shadow replay fails, the commit is aborted and no migration file is written.

## What gets replayed

For each commit with a shadow URL, mmmigrate:

1. Runs the dialect's reset SQL against the shadow database (drops and recreates the schema/tables).
2. Applies every committed migration in order.
3. Applies `current.sql` (with `@include` directives expanded).
4. If any step fails, aborts the commit.

This catches problems that only show up on a clean install, like:

- A migration that assumes a column exists from a manual hot-fix.
- Two migrations that interact badly when run from scratch but happen to work on the dev DB.
- Includes that reference functions defined in another include that no longer exists.
