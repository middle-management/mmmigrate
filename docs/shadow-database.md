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
2. Loads any [baseline](#objects-owned-by-another-app) files, in order.
3. Applies every committed migration in order.
4. Applies `current.sql` (with `@include` directives expanded).
5. If any step fails, aborts the commit.

This catches problems that only show up on a clean install, like:

- A migration that assumes a column exists from a manual hot-fix.
- Two migrations that interact badly when run from scratch but happen to work on the dev DB.
- Includes that reference functions defined in another include that no longer exists.

## Objects owned by another app

The reset empties the whole shadow database, so a chain that depends on objects it doesn't create, such as a foreign key or a grant on another app's schema in a shared database, fails on the replay. Pass that app's schema as a baseline. mmmigrate loads it after the reset and before the replay, and doesn't record it as an applied migration:

=== "Flag"

    ```bash
    pg_dump --schema-only --no-owner --no-privileges -n studio "$STUDIO_DATABASE_URL" > studio-schema.sql

    mmmigrate commit -description "add membership cache" \
      -shadow-url "postgres://localhost/accounts_shadow" \
      -shadow-baseline studio-schema.sql
    ```

=== "Environment variable"

    ```bash
    export MMMIGRATE_SHADOW_BASELINE=studio-schema.sql
    mmmigrate commit -description "add membership cache" \
      -shadow-url "postgres://localhost/accounts_shadow"
    ```

- **Repeatable.** Pass `-shadow-baseline` more than once, or separate paths in `MMMIGRATE_SHADOW_BASELINE` like `PATH` (`:` on Unix, `;` on Windows). Files load in the order given.
- **Plain SQL.** A baseline is run as one script, without `@include` processing, so the path can be anywhere, including outside the migrations directory.
- **Session settings don't leak.** Each baseline load runs on its own connection, which is then closed, so settings a dump makes (pg_dump empties `search_path`) don't reach the replay.
- **Roles live outside the database.** On PostgreSQL, roles belong to the server, not the shadow database, so the reset doesn't drop them and a baseline doesn't need to create them. Grants in your migrations, or owners and grants in a dump, need those roles to exist on the shadow's server. `--no-owner --no-privileges` keeps a dump free of them.
- **Keep it current.** The baseline stands in for what the deployed database holds before your migrations run. Regenerate it when the other app's schema changes.

`-shadow-baseline` needs a shadow URL. The environment variable is ignored when there's no shadow URL, so it can be set once for an app.
