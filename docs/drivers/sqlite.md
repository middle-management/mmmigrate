# SQLite

The SQLite driver uses [`modernc.org/sqlite`](https://pkg.go.dev/modernc.org/sqlite) — a pure-Go SQLite implementation, so no CGo is required. mmmigrate binaries are statically linked and trivially cross-compilable.

## Install

```bash
go install -tags sqlite github.com/middle-management/mmmigrate/cmd/mmmigrate@latest
```

## Connection string

Use a file path or the `:memory:` sentinel:

```bash
mmmigrate apply -database-url "file:./app.db"
mmmigrate apply -database-url ":memory:"     # tests only
```

The `DATABASE_URL` environment variable is honored as well.

## Tracking tables

| Table | Purpose |
|------|---------|
| `mmmigrate_applied` | One row per applied numbered migration |
| `mmmigrate_current` | Tracks the checksum of `current.sql` |

SQLite doesn't have schemas, so the tables live in the main database namespace with an `mmmigrate_` prefix.

## Locking

SQLite serializes writes through native **file-level locking**, which is built into the database engine. mmmigrate doesn't need to acquire an explicit lock — the underlying SQLite library handles concurrent access correctly across processes.

If you run `mmmigrate apply` in two processes against the same file, one will block on the SQLite write lock until the other finishes.

## Transactional DDL

SQLite supports transactional DDL: schema changes can be rolled back. This makes the `commit` dry-run safe — a failing migration leaves the database unchanged.

## Tips

- **Use SQLite for tests.** A `:memory:` database paired with mmmigrate gives you fast, isolated schema fixtures without any external dependencies.
- **Watch for SQLite's column constraints.** Older SQLite versions don't support `ALTER TABLE ... ADD CONSTRAINT` or modifying column types — you may need the rename-table-and-recopy pattern for non-trivial migrations.
- **`CREATE INDEX IF NOT EXISTS`** and `CREATE TABLE IF NOT EXISTS` work well for the `current.sql` idempotency requirement.
