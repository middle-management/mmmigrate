# Concurrency

mmmigrate is designed for environments where multiple processes — for example, several pods rolling out a new release — may try to run `mmmigrate apply` against the same database simultaneously. Each driver uses a database-level lock so only one mmmigrate instance applies migrations at a time.

| Dialect | Lock mechanism |
|---------|----------------|
| PostgreSQL | `pg_advisory_lock(1833701705, hashtext('<namespace>'))` (advisory lock keyed to a fixed integer plus a hash of the namespace) |
| MySQL | `GET_LOCK(CONCAT('mmmigrate:', LEFT(SHA2('<namespace>', 256), 40)), -1)` (named lock, blocking wait) |
| SQLite | Native file-level locking via the `sqlite3` driver |
| Spanner | A lease row in `mmmigrate_lock`, taken by a conditional write, renewed while held, expiring if not ([below](#spanner-leases)) |

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
- **Locks are released on disconnect.** PostgreSQL, MySQL and SQLite release the lock when the connection closes, so a crashed or killed mmmigrate process won't leave a stale lock. On Spanner a crashed run's lease expires instead (after 60 seconds by default).
- **The lock does not protect application traffic.** It only serializes mmmigrate processes. Your application can still run queries against the database during a migration.

## Spanner leases

Spanner has no advisory locks, session locks or `GET_LOCK`, so a run can't hold a lock that the database releases when its connection goes away. The Spanner driver uses a **lease**: a row per namespace in `mmmigrate_lock` naming the run that holds it and when the hold expires.

There were two ways to take it: a serializable read-modify-write (read the row in a read-write transaction, decide, write it back), or a **conditional write**. The driver uses the conditional write:

```sql
UPDATE mmmigrate_lock
SET owner = $1, expires_at = spanner.timestamptz_add(CURRENT_TIMESTAMP, '60000 milliseconds')
WHERE namespace = $2 AND (owner = '' OR expires_at < CURRENT_TIMESTAMP)
```

- It is one statement, which Spanner runs as a serializable transaction of its own, so the check and the write are atomic: of several runs, exactly one changes the row, and the rows-affected count tells each whether it was the one. A read-modify-write would be the same transaction spelled in three statements, with the driver deciding between them.
- It needs nothing beyond autocommit DML, which every driver supports the same way: no explicit transaction, no retry loop around an abort. Runs racing for the row may still abort one another; the loser polls again, as if the lease were held.
- Times are Spanner's (`CURRENT_TIMESTAMP`), so the clocks of the hosts running mmmigrate don't matter.

The row is created (`INSERT … ON CONFLICT (namespace) DO NOTHING`) before the first attempt. A run that doesn't get the lease polls with backoff, up to two seconds apart.

While a run holds the lease it renews it every third of the lease's length (`spanner.Dialect{LeaseTTL: …}`, 60 seconds by default), with `UPDATE … WHERE namespace = $1 AND owner = $2`, on a connection of its own from the pool: a run's own connection may be busy with a DDL batch for minutes. Releasing sets `owner` back to `''`.

What can go wrong, and what happens:

| Event | Outcome |
|-------|---------|
| The run dies | Its lease expires; the next run waits at most `LeaseTTL`, then takes it. A migration it left part-way is resumed ([Spanner: interrupted migrations](drivers/spanner.md#interrupted-migrations)). |
| A renewal fails (network, no free connection) | The next tick tries again. If none has been confirmed by the time the last confirmed lease could expire (less a tenth of `LeaseTTL`), the run's context is cancelled with `spanner.ErrLeaseLost` and it stops at its next statement: another run may take the lease from then on. |
| The run stalls past expiry and another takes the lease | The stalled run's next renewal changes no row, or its deadline passes: it stops the same way. A DDL batch already sent to Spanner completes regardless; the new holder resumes the migration in recovery mode. Every transaction that counts progress or records a migration checks the lease first, so the stalled run can't commit either before it notices. |
| The pool has a single connection | Renewals wait for the run's connection, so a run longer than `LeaseTTL` stops when its renewal deadline passes. Give the pool at least two connections. |

A lease only excludes runs that cooperate; like the other dialects' locks, it doesn't protect application traffic.

See the per-driver pages for more detail: [PostgreSQL](drivers/postgres.md), [SQLite](drivers/sqlite.md), [MySQL](drivers/mysql.md), [Spanner](drivers/spanner.md).
