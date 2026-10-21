# Concurrency

mmmigrate is designed for environments where multiple processes — for example, several pods rolling out a new release — may try to run `mmmigrate apply` against the same database simultaneously. Each driver uses a database-level lock so only one mmmigrate instance applies migrations at a time.

| Dialect | Lock mechanism |
|---------|----------------|
| PostgreSQL | `pg_advisory_lock(1833701705, hashtext('<namespace>'))` (advisory lock keyed to a fixed integer plus a hash of the namespace) |
| MySQL | `GET_LOCK(CONCAT('mmmigrate:', LEFT(SHA2('<namespace>', 256), 40)), -1)` (named lock, blocking wait) |
| SQLite | Native file-level locking via the `sqlite3` driver |

Locks are per [namespace](namespaces.md). Applies in the same namespace serialize; applies in different namespaces (say, two apps sharing a database) don't block each other. SQLite is the exception: file-level locking can't tell namespaces apart, so those applies still take turns.

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
