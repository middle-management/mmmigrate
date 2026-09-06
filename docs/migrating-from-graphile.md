# Migrating from Graphile Migrate

mmmigrate borrows the `current.sql` workflow from [Graphile Migrate](https://github.com/graphile/migrate) but differs in several important ways. If you're coming from Graphile, this page covers the conceptual mapping and the porting steps.

## Feature comparison

|  | Graphile Migrate | mmmigrate |
|---|---|---|
| **Language** | Node.js | Go (single binary, no runtime) |
| **Databases** | PostgreSQL only | PostgreSQL, SQLite, and MySQL via pluggable drivers |
| **Layout** | `current.sql` + `committed/NNNNNN-name.sql` | Flat by default, or the same `committed/` layout via `-committed` |
| **Integrity** | SHA-1 hash chain (`--! Hash:`) | SHA-256 checksums + merkle chain (`-- Chain:`) |
| **Includes** | `--! include` from a fixtures folder | `-- @include` from migrations subdirectories, restored on revert |
| **Shadow DB** | Required, auto-created via root DB connection | Optional (`-shadow-url`), user-managed |
| **Concurrency** | Advisory lock | Advisory lock (PostgreSQL), named lock (MySQL), file lock (SQLite) |
| **`current.sql`** | Must be idempotent; re-run on every file save (watch mode) | Must be idempotent; re-run when checksum changes |
| **Watch mode** | Yes (auto-applies on file change) | Yes (`mmmigrate watch`) or explicit `apply -current` |
| **Placeholders** | `:PLACEHOLDER_NAME` substitution in SQL | Not supported |
| **Hooks** | `beforeReset`, `afterReset`, `beforeAll`, `afterAll`, etc. | Not supported |
| **Down migrations** | Not supported (forward-only) | Not supported (forward-only) |
| **Usable as library** | Undocumented, not a public API | Yes — `mmmigrate` and `source` packages with `database/sql` |

## Porting your project

### 1. Install the right driver build

Graphile Migrate is PostgreSQL-only. If that's still your target, install:

```bash
go install -tags postgres github.com/middle-management/mmmigrate/cmd/mmmigrate@latest
```

### 2. Point mmmigrate at your migrations directory

Graphile's default layout is `migrations/current.sql` plus `migrations/committed/000001-name.sql`. mmmigrate defaults to a flat directory, with numbered migrations alongside `current.sql`:

```
migrations/
├── current.sql
├── 001_initial_schema.sql
├── 002_add_users.sql
└── ...
```

Both tools default to a `migrations/` directory and both keep the development file at `migrations/current.sql`, so nothing has to move. For the committed files you have two options.

**Keep Graphile's layout.** `-committed` names a subdirectory to read numbered migrations from:

```bash
mmmigrate apply -committed committed
export MMMIGRATE_COMMITTED=committed   # or set it once, for CI and local shells
```

Every command that touches committed migrations accepts the flag (`init`, `apply`, `baseline`, `commit`, `revert`, `status`, `validate`, `watch`). The value is a path relative to `-migrations` — so with `-migrations db/migrations` the example above reads `db/migrations/committed/`. `current.sql` and `@include` paths always resolve from the migrations root regardless, and with `-committed` set, loose `.sql` files at the root are treated as includable fixtures rather than migrations.

**Or flatten it:**

```bash
git mv migrations/committed/*.sql migrations/
rmdir migrations/committed
```

Either way, **the filenames can stay as they are**. mmmigrate accepts `-` as well as `_` between the version and the name, so `000001-initial-schema.sql` parses as version 1. Your next `mmmigrate commit` writes mmmigrate's own `NNN_name.sql` form and continues the numbering — commit after `000006-add-users.sql` and you get `007_<description>.sql`. Ordering is by parsed version, so the mixed padding is cosmetic.

Graphile's `--! Previous:` and `--! Hash:` headers are ordinary SQL comments to mmmigrate. `validate` skips files that carry no mmmigrate `-- Checksum:` header rather than failing on them, so carried-over migrations validate as-is and the merkle chain starts at your first mmmigrate commit.

What does have to change is the *content* of `current.sql` — includes and placeholders, covered next.

### 3. Convert include directives

Graphile uses `--! include` from a fixtures folder; mmmigrate uses `@include` from any subdirectory under `migrations/`:

```diff
- --! include functions/notify_event.sql
+ -- @include functions/notify_event.sql
```

Move included SQL files from Graphile's `fixtures/` directory to a subdirectory under `migrations/` (for example `migrations/functions/`).

### 4. Replace placeholders with environment-driven SQL

mmmigrate doesn't support `:PLACEHOLDER` substitution. If your migrations use placeholders for things like the application database name or schema, two options:

- **Hardcode** them — many placeholders are environment-specific only because Graphile Migrate generates them. mmmigrate's tracking tables don't care about your application's schema name.
- **Pre-process** the SQL with `envsubst` or a small wrapper script before feeding it to mmmigrate.

### 5. Replace lifecycle hooks

mmmigrate has no `beforeReset`/`afterReset`/`beforeAll`/`afterAll` equivalents. Most uses fall into a few patterns:

- **Seed data** — write a separate script that runs after `mmmigrate apply`. Don't put seed data in migrations.
- **Reset hooks** — call your own scripts before/after pointing mmmigrate at a shadow database.
- **Permission/role setup** — include the `GRANT`/`REVOKE` SQL directly in a migration.

### 6. Baseline your existing databases

This is the step that bites. mmmigrate decides what to run purely from its own tracking table — `mmmigrate.applied` on PostgreSQL, `mmmigrate_applied` on SQLite and MySQL — and that table starts empty. Point a fresh mmmigrate at a database Graphile has already migrated and it replays the chain from the top:

```
Error: failed to execute migration 1 (initial): ERROR: relation "users" already exists
```

`baseline` records migrations as applied **without executing their SQL**:

```bash
mmmigrate baseline -all           # every migration on disk
mmmigrate baseline -version 6     # everything up to and including version 6
```

Run it once against each database that already has the schema — production, staging, every developer's local copy — after deploying the mmmigrate binary and before the first `apply`. Use `-version N` where a database is behind: anything above `N` stays pending and applies normally on the next `apply`.

`baseline` never inspects the schema. It takes your word that those migrations are already reflected in the database, so compare `mmmigrate status` against Graphile's `graphile_migrate.migrations` table before running it. Re-running is safe — already-recorded versions are left alone.

A brand-new database needs no baseline; `apply` builds it from nothing.

### 7. Re-set up shadow database

Graphile auto-creates the shadow database via a root connection; mmmigrate expects it to exist. Create it once:

```bash
createdb myapp_shadow
export SHADOW_DATABASE_URL="postgres://localhost/myapp_shadow"
```

mmmigrate will reset and replay it on every `mmmigrate commit -shadow-url ...`. See [Shadow database](shadow-database.md) for details.

### 8. Update CI

Replace any `graphile-migrate` invocations in CI:

```bash
# Before:
graphile-migrate migrate

# After:
mmmigrate apply
mmmigrate check && mmmigrate validate
```

If you kept the `committed/` layout, set `MMMIGRATE_COMMITTED=committed` in the CI environment so every invocation picks it up.

## What you gain

- **Single binary, no Node runtime.** Deploy mmmigrate alongside your Go services or as a small static binary anywhere.
- **SQLite and MySQL support.** Useful for tests (SQLite) and projects on managed MySQL.
- **A documented library API.** Embed mmmigrate directly in your Go application.
- **A merkle chain.** Stronger tamper detection than Graphile's hash-each-file approach.
- **An adoption path.** `baseline` lets an existing database start being tracked by mmmigrate without replaying its history.

## What you give up

- **Placeholder substitution.** Not supported.
- **Lifecycle hooks.** Not supported — handle them outside the migration tool.
- **Auto-managed shadow DB.** You create and own the shadow database.
