# FAQ

## Common mistakes

| Mistake | Fix |
|---------|-----|
| Editing a committed migration | Never modify committed files — checksums and chain will fail. Use `revert` then re-commit. |
| Forgetting `-current` in dev | Without it, `apply` only runs numbered migrations, not `current.sql`. |
| Expecting `revert` to undo DB changes | `revert` is file-only. Write corrective SQL and commit a new migration. |
| Committing without a database | `commit` tests SQL against a real database — provide `DATABASE_URL` (or `-skip-verify` if you really mean it). |
| Non-idempotent `current.sql` | `current.sql` is re-run when it changes — use `IF NOT EXISTS`, `CREATE OR REPLACE`, etc. |
| Duplicate version numbers | Two files with the same `NNN_` prefix will error on load. |
| MySQL `commit` leaves dirty state | DDL can't be rolled back — use `-shadow-url` for safe verification. |

## How do I undo an applied migration?

You don't. mmmigrate is forward-only. Write corrective SQL in `current.sql` and commit a new migration that reverses or fixes the previous change.

If a migration that's already in production is wrong, the canonical fix is:

1. Add the corrective SQL to `current.sql`.
2. `mmmigrate apply -current` to verify on dev.
3. `mmmigrate commit -description "fix X"` to seal the correction.
4. Deploy.

`mmmigrate revert` only un-commits the file — it does not roll back the database.

## How do I rename a migration after committing it?

You can't rename it without breaking the chain. The migration's filename, body, and order all feed into the chain hash. If the description is genuinely wrong, `mmmigrate revert` it, fix the description, and `mmmigrate commit` again — but only do this **before** the migration has been applied to any shared environment.

## Can I edit a committed migration if I haven't deployed it yet?

Same answer: revert, edit, re-commit. Never edit the file directly.

## How do I seed initial data?

Don't put seed data in migrations. Write a separate seeding script that runs after `mmmigrate apply`. Migrations are for schema; seed data belongs to your application's bootstrap code.

If you really need data that's part of the schema (like enum lookup rows), use `INSERT ... ON CONFLICT DO NOTHING` and accept that it'll only run once per environment.

## Can I have multiple `current.sql` files?

No. There's exactly one drafting surface per migrations directory. If you want concurrent feature branches with separate schema changes, that's what `git` is for — keep each branch's `current.sql` in its branch and resolve at merge time.

## What happens if two developers commit at the same time?

Whoever pushes second sees a chain conflict at PR time: their migration's chain hash was computed against the now-stale tip. They `revert`, pull main, and `commit` again to recompute the chain.

Some teams prevent this with branch protection that auto-rebases migrations onto main. Most just communicate.

## How do I include a `.sql` file from outside `migrations/`?

You can't. `@include` paths are restricted to the migrations directory. If you have shared SQL in another part of your repo, copy or symlink it into `migrations/`.

## Why does `mmmigrate apply` block?

It's waiting on the [concurrency lock](concurrency.md). Another mmmigrate process — possibly from a parallel pod, deploy, or stray dev session — is already applying. Wait, or kill the other process to release the lock.

## What database/version support is tested?

The CI matrix runs against:

- PostgreSQL 17 (older versions work; advisory locks have been stable forever).
- MySQL 8.
- SQLite via `modernc.org/sqlite` (latest).

See [`.github/workflows/ci.yml`](https://github.com/middle-management/mmmigrate/blob/main/.github/workflows/ci.yml) for the canonical matrix.

## Where is the tracking table?

| Driver | Location |
|--------|----------|
| PostgreSQL | Schema `mmmigrate`, tables `applied` and `current` |
| SQLite | Tables `mmmigrate_applied` and `mmmigrate_current` |
| MySQL | Tables `mmmigrate_applied` and `mmmigrate_current` |

If you need to inspect what mmmigrate thinks it's applied, query those directly.

## Can I run mmmigrate as a library inside my Go service?

Yes — see [Library API](library.md). `mmmigrate.RunMigrations(ctx, db, dialect, "migrations", false)` on app startup is a common pattern.
