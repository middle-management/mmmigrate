# PostgreSQL

The PostgreSQL driver uses the [`pgx`](https://github.com/jackc/pgx) standard library adapter.

## Install

```bash
go install -tags postgres github.com/middle-management/mmmigrate/cmd/mmmigrate@latest
```

## Connection string

Pass the connection string via `-database-url` or the `DATABASE_URL` environment variable. mmmigrate accepts the standard PostgreSQL URL format:

```
postgres://user:password@host:5432/dbname?sslmode=disable
```

## Tracking tables

The PostgreSQL driver creates a dedicated schema for its bookkeeping:

| Table | Purpose |
|------|---------|
| `mmmigrate.applied` | One row per applied numbered migration |
| `mmmigrate.current` | Tracks the checksum of `current.sql` so it's only re-run on change |

Using a separate schema keeps mmmigrate's tables out of `public` and clear of your application's namespace.

## Locking

Concurrent applies are serialized through a PostgreSQL **advisory lock** keyed to the integer `1833701705`:

```sql
SELECT pg_advisory_lock(1833701705);
-- ... apply migrations ...
SELECT pg_advisory_unlock(1833701705);
```

The lock is automatically released if the connection is dropped.

## Transactional DDL

PostgreSQL supports transactional DDL: `CREATE TABLE`, `ALTER TABLE`, `CREATE INDEX`, etc., all participate in transactions. This means:

- The `commit` dry-run can safely test schema changes against your dev database — they're rolled back when the test transaction ends.
- A migration that fails partway through is fully rolled back. No partial state.

A few statements still run outside of transactions in PostgreSQL — most notably `CREATE INDEX CONCURRENTLY`. mmmigrate runs each migration inside a transaction, so concurrent index creation isn't supported in a single migration. Split it out into its own migration that doesn't need transactional safety, or run it manually.

## Tips

- **Use `IF NOT EXISTS`** on `CREATE TABLE`/`CREATE INDEX` in `current.sql` so it's safe to re-run while iterating.
- **Use `CREATE OR REPLACE`** for functions and views.
- **Pair with a shadow database** in CI (`-shadow-url`) for an extra safety net, even though it's not strictly required.
