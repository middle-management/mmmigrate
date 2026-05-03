# Workflow

mmmigrate has one core loop: edit `current.sql` in development, commit it as a numbered migration, then apply in production.

```
edit current.sql → apply -current (dev, iterate) → commit → apply (prod)
                                                    ↕
                                                  revert (files only)
```

## The four states of a migration

| Stage | Where the SQL lives | How it runs |
|------|--------------------|-------------|
| Drafting | `migrations/current.sql` | `apply -current` re-runs whenever the checksum changes |
| Committed | `migrations/NNN_description.sql` | `apply` runs it once, in order, then never again |
| Reverted | Back in `migrations/current.sql` | Files-only — the database is **not** rolled back |
| Tampered | Anywhere | `validate` fails on the broken chain |

## Editing `current.sql`

`current.sql` is your scratch pad. Write SQL there, run `mmmigrate apply -current` against your dev database, see if it works, edit, repeat.

Because mmmigrate re-runs `current.sql` whenever its checksum changes, **it must be idempotent**. Use `IF NOT EXISTS` clauses, `CREATE OR REPLACE` for functions and views, and avoid `INSERT`s without `ON CONFLICT` handling.

```sql
-- Good: idempotent
CREATE TABLE IF NOT EXISTS users (id INTEGER PRIMARY KEY, name TEXT);
CREATE OR REPLACE FUNCTION audit_user() ...

-- Bad: fails on the second run
CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT);
INSERT INTO seed_settings(k, v) VALUES ('theme', 'dark');
```

## Committing

When you're ready to lock the change in:

```bash
mmmigrate commit -description "add users table"
```

The commit step:

1. Verifies `current.sql` parses and runs against the dev database (inside a transaction, rolled back).
2. Computes a SHA-256 checksum of the body and a chain hash linking it to the previous migration.
3. Writes `migrations/NNN_add_users_table.sql` with both hashes embedded as comments.
4. Expands `@include` directives inline so the committed file is fully self-contained.
5. Empties `current.sql` for your next change.

See [Integrity](integrity.md) for what those hashes do, and [Shadow database](shadow-database.md) for the recommended way to verify migrations on MySQL.

## Reverting

Made a mistake before pushing? Revert un-commits the latest migration:

```bash
mmmigrate revert
```

This is a **file operation only**. It moves `migrations/NNN_*.sql` back into `current.sql`, restoring `@include` directives that were expanded during commit. The database is untouched — already-applied changes stay applied.

To fix a migration that's already been deployed, write corrective SQL in `current.sql` and commit a new migration. mmmigrate is forward-only by design — there are no down migrations.

## Watching

For a tight inner loop, use `watch`:

```bash
mmmigrate watch                       # default 200ms debounce
mmmigrate watch -debounce 500ms       # coalesce bursts of editor saves
```

`watch` does an initial apply, then re-applies `current.sql` (and any `@include`d files) on every save. It picks up newly added `@include` directives automatically.

This is equivalent to running `apply -current` on every save. Stop with `Ctrl-C`.

## Production

In production, never use `-current`. Run only the committed numbered migrations:

```bash
DATABASE_URL="postgres://prod/myapp" mmmigrate apply
```

mmmigrate skips migrations that have already been recorded in the tracking table, so it's safe to run on every deploy.

For CI, pair `apply` with [`check`](commands.md#check) to fail builds when `current.sql` has uncommitted changes:

```bash
mmmigrate check && mmmigrate validate
```
