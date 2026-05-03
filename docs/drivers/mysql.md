# MySQL

The MySQL driver uses [`go-sql-driver/mysql`](https://github.com/go-sql-driver/mysql). It also works with MariaDB and any reasonably modern fork.

## Install

```bash
go install -tags mysql github.com/middle-management/mmmigrate/cmd/mmmigrate@latest
```

## Connection string

mmmigrate uses the standard `go-sql-driver/mysql` DSN format:

```
user:password@tcp(host:3306)/dbname?multiStatements=true&parseTime=true
```

!!! tip "`multiStatements=true` is recommended"
    mmmigrate runs each migration as a single SQL string. Without `multiStatements=true`, MySQL rejects scripts containing more than one statement.

## Tracking tables

| Table | Purpose |
|------|---------|
| `mmmigrate_applied` | One row per applied numbered migration |
| `mmmigrate_current` | Tracks the checksum of `current.sql` |

MySQL uses an `mmmigrate_` table-name prefix rather than a dedicated schema.

## Locking

Concurrent applies are serialized through a MySQL **named lock**:

```sql
SELECT GET_LOCK('mmmigrate', -1);
-- ... apply migrations ...
SELECT RELEASE_LOCK('mmmigrate');
```

`-1` means "wait indefinitely". The lock is released automatically when the connection closes.

## DDL caveats

MySQL/MariaDB DDL (`CREATE TABLE`, `ALTER TABLE`, `CREATE INDEX`, etc.) causes an **implicit commit** and **cannot be rolled back** within a transaction. This affects mmmigrate in two ways:

### `commit` dry-run cannot roll back DDL

The normal `commit` flow runs `current.sql` inside a transaction and rolls it back. On MySQL, DDL inside that transaction commits anyway — leaving your dev database with a half-applied schema change.

**Use a [shadow database](../shadow-database.md)** to verify migrations against a disposable database instead:

```bash
mmmigrate commit -description "..." -shadow-url "user:password@tcp(localhost:3306)/myapp_shadow"
```

`SHADOW_DATABASE_URL` is honored too. The shadow database is wiped on every commit and replays the entire migration chain from scratch.

### Partial failures cannot be undone

If a migration with multiple DDL statements fails partway through, the completed statements stay in place. A naïve `apply` retry will then fail on the already-completed statements.

**Keep migrations small** — ideally one DDL statement per migration. That way, a partial failure is also a complete failure, and `apply` can retry cleanly.

## Tips

- **Always set `-shadow-url` (or `SHADOW_DATABASE_URL`)** before committing on MySQL. Treat the shadow database as required infrastructure, not optional.
- **Use small migrations.** One `CREATE TABLE` or one `ALTER TABLE` per commit keeps the blast radius of a partial failure minimal.
- **Pre-create the shadow database.** mmmigrate resets and replays — it does not create the database itself.

PostgreSQL and SQLite both support transactional DDL and don't have these limitations. See [Shadow database](../shadow-database.md) for the full story.
