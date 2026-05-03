# Concurrency

mmmigrate is designed for environments where multiple processes — for example, several pods rolling out a new release — may try to run `mmmigrate apply` against the same database simultaneously. Each driver uses a database-level lock so only one mmmigrate instance applies migrations at a time.

| Dialect | Lock mechanism |
|---------|----------------|
| PostgreSQL | `pg_advisory_lock` (advisory lock keyed to a fixed integer) |
| MySQL | `GET_LOCK('mmmigrate', -1)` (named lock, blocking wait) |
| SQLite | Native file-level locking via the `sqlite3` driver |

## How it works in practice

When `mmmigrate apply` starts, it:

1. Acquires the dialect's lock (blocking until available).
2. Reads the tracking table to determine which migrations are already applied.
3. Runs pending migrations in order, each in its own transaction (where supported).
4. Releases the lock.

If a second process starts during step 2–3, it blocks at step 1 until the first finishes. By the time it reaches step 2, the migrations are recorded as applied and it has nothing to do — making concurrent applies safe and idempotent.

## Implications

- **Multi-pod rollouts are safe.** Every pod can run `mmmigrate apply` on startup; only one will actually do the work.
- **Long migrations block other deploys.** A migration that takes 10 minutes will hold the lock for 10 minutes. New pods that try to apply will wait.
- **Locks are released on disconnect.** All three dialects release the lock when the connection closes, so a crashed or killed mmmigrate process won't leave a stale lock.
- **The lock does not protect application traffic.** It only serializes mmmigrate processes. Your application can still run queries against the database during a migration.

See the per-driver pages for more detail: [PostgreSQL](drivers/postgres.md), [SQLite](drivers/sqlite.md), [MySQL](drivers/mysql.md).
