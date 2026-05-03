# Migrating from Graphile Migrate

mmmigrate borrows the `current.sql` workflow from [Graphile Migrate](https://github.com/graphile/migrate) but differs in several important ways. If you're coming from Graphile, this page covers the conceptual mapping and the porting steps.

## Feature comparison

|  | Graphile Migrate | mmmigrate |
|---|---|---|
| **Language** | Node.js | Go (single binary, no runtime) |
| **Databases** | PostgreSQL only | PostgreSQL, SQLite, and MySQL via pluggable drivers |
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

### 2. Move your migrations directory

Graphile's default layout is `migrations/current.sql` plus `migrations/committed/NNNNNNNNNN-name.sql`. mmmigrate keeps everything flat under `migrations/`:

```
migrations/
├── current.sql
├── 001_initial_schema.sql
├── 002_add_users.sql
└── ...
```

You'll need to:

- Move `current.sql` to the new path (often unchanged).
- Either re-commit your committed migrations from scratch (recommended for a clean chain), or rewrite the headers to match mmmigrate's format and recompute checksums.

For most projects, re-committing is simpler: drop your existing `committed/` directory, snapshot your production schema as the new `001`, and start the chain fresh.

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

### 6. Re-set up shadow database

Graphile auto-creates the shadow database via a root connection; mmmigrate expects it to exist. Create it once:

```bash
createdb myapp_shadow
export SHADOW_DATABASE_URL="postgres://localhost/myapp_shadow"
```

mmmigrate will reset and replay it on every `mmmigrate commit -shadow-url ...`. See [Shadow database](shadow-database.md) for details.

### 7. Update CI

Replace any `graphile-migrate` invocations in CI:

```bash
# Before:
graphile-migrate migrate

# After:
mmmigrate apply
mmmigrate check && mmmigrate validate
```

## What you gain

- **Single binary, no Node runtime.** Deploy mmmigrate alongside your Go services or as a small static binary anywhere.
- **SQLite and MySQL support.** Useful for tests (SQLite) and projects on managed MySQL.
- **A documented library API.** Embed mmmigrate directly in your Go application.
- **A merkle chain.** Stronger tamper detection than Graphile's hash-each-file approach.

## What you give up

- **Placeholder substitution.** Not supported.
- **Lifecycle hooks.** Not supported — handle them outside the migration tool.
- **Auto-managed shadow DB.** You create and own the shadow database.
