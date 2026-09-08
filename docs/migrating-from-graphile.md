# Migrating from Graphile Migrate

mmmigrate borrows the `current.sql` workflow from [Graphile Migrate](https://github.com/graphile/migrate) but differs in several important ways. If you're coming from Graphile, this page covers the conceptual mapping and the porting steps.

## Feature comparison

|  | Graphile Migrate | mmmigrate |
|---|---|---|
| **Language** | Node.js | Go (single binary, no runtime) |
| **Databases** | PostgreSQL only | PostgreSQL, SQLite, and MySQL via pluggable drivers |
| **Layout** | `current.sql` + `committed/000001-name.sql` | Flat: `current.sql` + `001_name.sql`, all in `migrations/` |
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

Every step below is a concrete command, and each one ends with a check you can run. Work through them in order — step 6 in particular must happen before the first `mmmigrate apply` against any database that Graphile already migrated.

### 1. Install the right driver build

Graphile Migrate is PostgreSQL-only. If that's still your target, install:

```bash
go install -tags postgres github.com/middle-management/mmmigrate/cmd/mmmigrate@latest
```

### 2. Flatten and rename the committed migrations

Graphile keeps committed migrations in `migrations/committed/`, numbered `000001-name.sql`. mmmigrate keeps everything flat under `migrations/`:

```
migrations/
├── current.sql
├── 001_initial_schema.sql
├── 002_add_users.sql
└── fixtures/           # subdirectories hold @include-able files
```

`current.sql` keeps its name and location — both tools default to `migrations/current.sql`, so that file does not move. The committed files do, and they need renaming: mmmigrate splits the version from the name on the **first underscore**, so a Graphile filename fails to parse as-is:

```
Error: failed to load migrations: failed to parse migration name 000001-initial.sql:
invalid migration filename format: 000001-initial.sql (expected: NNN_name.sql)
```

This bash loop flattens and renames in one pass. It turns `000001-initial-schema.sql` into `001_initial_schema.sql`, and names an unnamed migration (`graphile-migrate commit` with no message writes `000001.sql`) `001_migration.sql`:

```bash
cd migrations/committed
for f in *.sql; do
  base=${f%.sql}
  version=${base%%-*}                       # 000001
  name=${base#"$version"}; name=${name#-}   # initial-schema, empty if unnamed
  name=${name//-/_}                         # initial_schema
  printf -v new '%03d_%s.sql' "$((10#$version))" "${name:-migration}"
  git mv "$f" "../$new"     # plain mv if the files aren't tracked
done
cd .. && rmdir committed
```

**Check:** `mmmigrate status` (or `mmmigrate validate`, which needs no database) should list every migration instead of erroring. Version numbers must be unique — mmmigrate rejects duplicates — and they are what determines order, so the renumbering from 6 digits to 3 is safe.

Leave the file *contents* alone. Graphile's `--! Previous:` and `--! Hash:` headers are ordinary SQL comments to mmmigrate, and `validate` skips any migration with no mmmigrate `-- Checksum:` header rather than failing on it. Your merkle chain simply starts at the first migration you commit with mmmigrate; that commit continues the numbering, so after `006_...` comes `007_<description>.sql`.

### 3. Convert include directives

Both tools inline a shared SQL file into `current.sql`, but they spell the directive differently and resolve the path from a different base:

| | Directive | Path is relative to |
|---|---|---|
| Graphile | `--!include user_view.sql` | `migrations/fixtures/` |
| mmmigrate | `-- @include fixtures/user_view.sql` | `migrations/` |

So the *files* stay where they are — `migrations/fixtures/` is already a subdirectory of the migrations directory, which is all mmmigrate requires — and only the directives change, gaining the `fixtures/` prefix:

```bash
sed -i -E 's|^([[:space:]]*)--![[:space:]]*include[[:space:]]+|\1-- @include fixtures/|' \
  migrations/current.sql
```

Run it over any nested includes too (files under `fixtures/` that include other files). Committed migrations need nothing: Graphile expanded their includes inline at commit time.

**Check:** `mmmigrate render` prints `current.sql` with every include expanded, and fails loudly on a path it cannot resolve.

### 4. Replace placeholders with environment-driven SQL

mmmigrate doesn't support `:PLACEHOLDER` substitution. If your migrations use placeholders for things like the application database name or schema, two options:

- **Hardcode** them — many placeholders are environment-specific only because Graphile Migrate generates them. mmmigrate's tracking tables don't care about your application's schema name.
- **Pre-process** the SQL with `envsubst` or a small wrapper script before feeding it to mmmigrate.

### 5. Replace lifecycle hooks

mmmigrate has no `beforeReset`/`afterReset`/`beforeAll`/`afterAll` equivalents. Most uses fall into a few patterns:

- **Seed data** — write a separate script that runs after `mmmigrate apply`. Don't put seed data in migrations.
- **Reset hooks** — call your own scripts before/after pointing mmmigrate at a shadow database.
- **Permission/role setup** — include the `GRANT`/`REVOKE` SQL directly in a migration.

### 6. Record existing migrations as applied

**Do this before the first `apply` against any database Graphile has already migrated.** mmmigrate works out what to run purely from its own tracking table, which starts empty, so it will otherwise replay your whole history against a live database and fail on the first statement:

```
Error: failed to execute migration 1 (initial): ERROR: relation "users" already exists
```

The fix is to tell mmmigrate those migrations are already in place. First let it create the tracking table — `mmmigrate status` does that and changes nothing else:

```bash
mmmigrate status     # creates the tracking table, lists every migration as pending
```

Then insert one row per migration that the database already has. `applied_at` defaults, and only `version` is matched against the files — `name` is for humans — so one statement covers every driver except for the table name:

```sql
-- PostgreSQL
INSERT INTO mmmigrate.applied (version, name) VALUES
  (1, 'initial_schema'),
  (2, 'add_users');

-- SQLite and MySQL: same statement against mmmigrate_applied
```

Generate the rows from the filenames rather than typing them, and cut the list off at the last migration the database actually has:

```bash
for f in migrations/[0-9]*.sql; do
  b=$(basename "$f" .sql)
  printf "  (%d, '%s'),\n" "$((10#${b%%_*}))" "${b#*_}"
done
```

Graphile's own record of what it applied is in `graphile_migrate.migrations` — compare against it before you decide where to stop. Anything you leave out stays pending and applies normally on the next `mmmigrate apply`.

**Check:** `mmmigrate status` shows `✓` against every migration you inserted, and `mmmigrate apply -dry-run` reports nothing to do. Repeat for every database that already has the schema: production, staging, and each developer's local copy. A brand-new database needs none of this — `apply` builds it from nothing.

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

## What you gain

- **Single binary, no Node runtime.** Deploy mmmigrate alongside your Go services or as a small static binary anywhere.
- **SQLite and MySQL support.** Useful for tests (SQLite) and projects on managed MySQL.
- **A documented library API.** Embed mmmigrate directly in your Go application.
- **A merkle chain.** Stronger tamper detection than Graphile's hash-each-file approach.

## What you give up

- **Placeholder substitution.** Not supported.
- **Lifecycle hooks.** Not supported — handle them outside the migration tool.
- **Auto-managed shadow DB.** You create and own the shadow database.
