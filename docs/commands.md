# Commands

All commands accept `-migrations DIR` (default: `migrations`). Database commands accept `-database-url URL`, defaulting to the `DATABASE_URL` environment variable.

## Reference

| Command | Needs DB | Description |
|---------|----------|-------------|
| [`init`](#init) | no | Create migrations directory and empty `current.sql` |
| [`apply`](#apply) | yes | Run pending migrations (`-current` includes `current.sql`, `-dry-run` shows what would run) |
| [`commit`](#commit) | yes\* | Test and commit `current.sql` as a numbered migration |
| [`revert`](#revert) | no | Uncommit last migration back to `current.sql` |
| [`status`](#status) | yes | Show which migrations are applied/pending |
| [`render`](#render) | no | Print `current.sql` with includes expanded |
| [`check`](#check) | no | Verify `current.sql` has no uncommitted changes |
| [`validate`](#validate) | no | Verify checksums and merkle chain integrity |
| [`watch`](#watch) | yes | Watch `current.sql` and re-apply on change |
| [`version`](#version) | no | Print the mmmigrate version |

\*`commit` does not need a database connection when `-skip-verify` is used.

## `init`

```bash
mmmigrate init
```

Creates the migrations directory (default `migrations/`) and an empty `current.sql`. Run this once per project.

## `apply`

```bash
mmmigrate apply                # production: committed migrations only
mmmigrate apply -current       # development: also runs current.sql
mmmigrate apply -dry-run       # print what would run, don't execute
mmmigrate apply -current -dry-run
```

Runs all pending committed migrations in version order. Already-applied migrations are skipped via the tracking table.

With `-current`, mmmigrate also runs `current.sql` after the numbered migrations. It's re-run only when the checksum changes since the last apply.

With `-dry-run`, mmmigrate prints the SQL it would execute without committing anything to the database.

## `commit`

```bash
mmmigrate commit -description "add events table"
mmmigrate commit -description "..." -shadow-url "postgres://localhost/myapp_shadow"
mmmigrate commit -description "..." -skip-verify
```

Tests `current.sql` against the database, then writes a numbered migration file:

1. Loads `current.sql` and expands all `@include` directives inline.
2. Runs the SQL inside a transaction and rolls back (where the dialect supports it).
3. If `-shadow-url` is set, additionally replays every committed migration plus the new one against the shadow database from a clean slate. See [Shadow database](shadow-database.md).
4. Computes the SHA-256 checksum of the body and the chain hash.
5. Writes `migrations/NNN_<description>.sql` with checksum/chain comments.
6. Empties `current.sql`.

`-skip-verify` skips both the dry-run and the shadow check. Use sparingly — it lets you commit SQL that has never been executed against any database.

`SHADOW_DATABASE_URL` is honored when `-shadow-url` is not passed.

## `revert`

```bash
mmmigrate revert
```

Moves the most recent committed migration back into `current.sql`, restoring any `@include` directives that were expanded on commit. The database is **not** modified — this is a file operation. To undo applied SQL, write corrective SQL and commit a new migration.

## `status`

```bash
mmmigrate status
```

Prints each migration with its applied/pending state and checksum. Useful for confirming production matches your repo before a deploy.

## `render`

```bash
mmmigrate render
mmmigrate render | psql "$DATABASE_URL"
```

Prints `current.sql` to stdout with all `@include` directives expanded. Doesn't touch the database. Handy for piping into `psql`/`sqlite3`/`mysql` when you want to apply current.sql by hand or inspect what would actually run.

## `check`

```bash
mmmigrate check
```

Exits non-zero if `current.sql` has any content that hasn't been committed yet. Use as a CI gate to prevent merging branches with un-committed schema changes.

## `validate`

```bash
mmmigrate validate
```

Recomputes the checksum of every committed migration and verifies that the merkle chain matches what's stored in each file. Fails if any migration was modified or if the chain was broken by inserting/reordering migrations. See [Integrity](integrity.md).

A common CI gate is `mmmigrate check && mmmigrate validate`.

## `watch`

```bash
mmmigrate watch                       # default 200ms debounce
mmmigrate watch -debounce 500ms
```

Watches `current.sql` and any `@include`d files. On change, re-runs `apply -current`. Picks up new include directives automatically after the next save. Stop with `Ctrl-C`.

## `version`

```bash
mmmigrate version
```

Prints the build version and exits.
