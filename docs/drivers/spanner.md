# Spanner

The Spanner driver runs migrations on [Cloud Spanner](https://cloud.google.com/spanner) databases of the **PostgreSQL dialect**. It works over any `database/sql` driver that reaches one, and imports none itself:

- **pgx through [PGAdapter](https://github.com/GoogleCloudPlatform/pgadapter)**, Spanner's PostgreSQL wire-protocol proxy. This is the default (`Dialect{}`), and what the CLI uses.
- **[go-sql-spanner](https://github.com/googleapis/go-sql-spanner)**, Spanner's own driver over gRPC: `spanner.Dialect{Driver: "spanner"}`.

Databases of Spanner's GoogleSQL dialect aren't supported.

## Install

```bash
go install -tags spanner github.com/middle-management/mmmigrate/cmd/mmmigrate@latest
```

## Connection string

The CLI connects through PGAdapter with a PostgreSQL URL; PGAdapter maps the database name to a Spanner database of the instance it was started for:

```bash
mmmigrate apply -database-url "postgres://user@localhost:5432/mydb?sslmode=disable"
```

From Go, open either driver yourself and pass the dialect:

=== "PGAdapter (pgx)"

    ```go
    import (
        _ "github.com/jackc/pgx/v5/stdlib"
        "github.com/middle-management/mmmigrate/driver/spanner"
    )

    db, _ := sql.Open("pgx", "postgres://user@localhost:5432/mydb?sslmode=disable")
    err := mmmigrate.RunMigrations(ctx, db, spanner.Dialect{}, fsys, false)
    ```

=== "go-sql-spanner"

    ```go
    import (
        _ "github.com/googleapis/go-sql-spanner"
        "github.com/middle-management/mmmigrate/driver/spanner"
    )

    db, _ := sql.Open("spanner", "projects/p/instances/i/databases/mydb")
    err := mmmigrate.RunMigrations(ctx, db, spanner.Dialect{Driver: "spanner"}, fsys, false)
    ```

## Tracking tables

| Table | Purpose |
|------|---------|
| `mmmigrate_applied` | One row per applied numbered migration, per [namespace](../namespaces.md) |
| `mmmigrate_current` | One row per namespace tracking the checksum of `current.sql` |
| `mmmigrate_progress` | One row per migration under way: how much of it has run (see [Interrupted migrations](#interrupted-migrations)) |
| `mmmigrate_lock` | One lease row per namespace (see [Locking](#locking)) |

They live in the default (`public`) schema with an `mmmigrate_` prefix, as on SQLite and MySQL: Spanner's named schemas aren't PostgreSQL's, and the tracking tables don't need one. `applied` and `progress` are created before anything else (they record the [self-migrations](index.md)), `lock` by the first lock taken, and `current` by the first self-migration.

## How migrations run

Spanner can't run DDL in a transaction, nor DDL and DML in the same one, so a migration can't be applied and recorded atomically as on PostgreSQL. The driver therefore runs each migration itself, outside transactions:

1. It splits the migration into statements and groups them: each run of consecutive DDL statements is one **batch** (`START BATCH DDL` … `RUN BATCH`), a single schema change on Spanner instead of one per statement, each of which takes seconds; every other statement runs on its own.
2. A DML statement runs in a transaction with the update of the migration's progress row, so it runs exactly once.
3. A DDL batch can't share a transaction with anything, so its progress is counted once the batch has completed.
4. Only when every statement has succeeded is the migration recorded in `mmmigrate_applied`, and its progress row deleted, in one transaction.

`CREATE TABLE IF NOT EXISTS` and `CREATE INDEX IF NOT EXISTS` of objects that already exist are skipped rather than sent: on Spanner even a schema change that changes nothing takes seconds.

### Interrupted migrations

If a migration fails, or the process dies, part-way, the statements that ran stay applied (DDL can't be rolled back), and the migration isn't recorded. Its row in `mmmigrate_progress` says how many statement groups completed, with a hash of their text. The next run:

- **resumes after the completed groups**, as long as their text is unchanged, so you can fix a later statement that failed and run again. If the completed part changed, the run stops and asks you to bring the database in line and delete the progress row.
- **re-runs the group it stopped in.** If that group is DML, it either committed with its progress or not at all, so it simply runs. If it is a DDL batch, it may have been under way, partly applied (Spanner keeps the statements of a batch that ran before one failed), or complete but not yet counted. So it runs in **recovery mode**: one statement at a time, each in its idempotent form where Spanner has one:

    | Written | Run as |
    |---------|--------|
    | `CREATE TABLE / INDEX / SEQUENCE / SCHEMA x` | `CREATE … IF NOT EXISTS x` |
    | `CREATE VIEW x` | `CREATE OR REPLACE VIEW x` |
    | `DROP TABLE / INDEX / SEQUENCE / SCHEMA / VIEW x` | `DROP … IF EXISTS x` |
    | `ALTER TABLE t ADD COLUMN c` | `ALTER TABLE t ADD COLUMN IF NOT EXISTS c` |

    Statements without such a form (`ALTER COLUMN`, `ADD CONSTRAINT`, `DROP COLUMN`, change streams) run as written, and fail if they had already taken effect. The error says so: check the database, and either undo the statement or set the progress row's `done` to the number the error gives, then run again.

In particular, a run that dies after a migration's DDL completed but before the migration was recorded is the last case: the next run repeats that DDL in recovery mode, which changes nothing, and records the migration.

!!! tip "Keep a migration's non-idempotent DDL to one statement group"
    Recovery covers the common statements. For the others, put each in a migration of its own, or last in its migration, so an interruption leaves at most one statement to check.

### `current.sql`

`current.sql` runs the same way, but keeps no progress: it is re-run whenever it changes. Write it to be re-runnable (`IF NOT EXISTS`), as on every database.

## Locking

Spanner has no advisory or session locks. Each run takes a **lease** on its namespace's row in `mmmigrate_lock` instead, with a single conditional write, which Spanner applies atomically:

```sql
UPDATE mmmigrate_lock
SET owner = $1, expires_at = spanner.timestamptz_add(CURRENT_TIMESTAMP, '60000 milliseconds')
WHERE namespace = $2 AND (owner = '' OR expires_at < CURRENT_TIMESTAMP)
```

Of several runs, exactly one sees its update change the row; the others poll (with backoff, up to 2 s apart) until it is released or expires. Expiry is by Spanner's clock, so the hosts' clocks don't matter. See [Concurrency](../concurrency.md#spanner-leases) for why a lease and how it behaves.

- **Lease length**: `Dialect{LeaseTTL: …}`, 60 seconds by default. The holder renews it every third of that, on a connection of its own from the pool, so a long DDL batch on the run's connection doesn't hold up the renewal. Give the pool at least two connections.
- **A run that dies** leaves its lease to expire: others wait at most `LeaseTTL`.
- **A run that loses its lease** (stalled past expiry, then taken over) finds out at its next renewal; its context is cancelled with `spanner.ErrLeaseLost`, so it stops at its next statement. The migration it was running is resumed by the next run, as above.

## Shadow database

`ResetSQL` is unused: shadow verification calls `Reset`, which reads the catalog and drops, in the order Spanner requires, every view, change stream, user index, foreign key, table (interleaved children first), sequence, and named schema, the tracking tables included. Point `-shadow-url` only at a disposable database.

`commit` can't test `current.sql` in a rolled-back transaction on Spanner, since DDL can't run in one. It requires `-shadow-url` instead (or `-skip-verify`):

```bash
mmmigrate commit -description "add widgets" \
  -shadow-url "postgres://user@localhost:5432/mydb_shadow?sslmode=disable"
```

## Spanner's SQL

Write migrations in Spanner's PostgreSQL dialect, which differs from PostgreSQL's. Among the differences that matter to schemas: integers are all `bigint` (`integer` is accepted as an alias of it, `smallint` isn't), no `UNIQUE` constraints (use unique indexes), partial indexes only as `WHERE col IS NOT NULL`, no `DO` blocks, functions or triggers, `ON CONFLICT` only with a conflict target, and `SERIALIZABLE` isolation only. See Google's [PostgreSQL interface reference](https://cloud.google.com/spanner/docs/reference/postgresql/overview).

## Testing against the emulator

The `pgadapter-emulator` image bundles the Spanner emulator and PGAdapter; PGAdapter creates a database the first time one is named:

```bash
docker run -d -p 5432:5432 -p 9010:9010 gcr.io/cloud-spanner-pg-adapter/pgadapter-emulator

# Through PGAdapter (pgx)
cd driver/spanner
MMMIGRATE_TEST_SPANNER_URL='postgres://x@localhost:5432/mmmigrate_test?sslmode=disable' go test ./...

# Through go-sql-spanner, on the emulator's gRPC port (a module of its own)
cd driver/spanner/gosqlspannertest
MMMIGRATE_TEST_SPANNER_EMULATOR_HOST=localhost:9010 go test ./...
```

Each test resets one database rather than create another: an instance holds at most 100 databases, and PGAdapter can't drop one.

The emulator differs from Spanner in ways the tests allow for: it refuses a schema change while another, or a read-write transaction, is under way (the driver retries those), and it rolls back a whole DDL batch when one statement fails, where Spanner keeps the statements before it.

## Driver notes

- go-sql-spanner binds `$n` parameters in the order they appear in the statement, not by number. The driver's own statements number them in order of appearance, each used once, so they bind the same under both drivers.
- PGAdapter replaces `information_schema.sequences` with an empty stand-in; the reset reads `"information_schema"."sequences"`, quoted, which it passes through.
