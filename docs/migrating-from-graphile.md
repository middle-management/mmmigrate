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

**Do this before the first `apply` against any database Graphile has already migrated.** mmmigrate works out what to run purely from its own tracking table, which starts empty, so it will otherwise replay your whole history against a live database. At best that fails on the first statement:

```
Error: failed to execute migration 1 (initial): ERROR: relation "users" already exists
```

At worst it succeeds. A first migration that starts with `DROP SCHEMA app CASCADE`, which is common in Graphile projects because `current.sql` must be idempotent, wipes the data.

The script below tells mmmigrate which migrations the database has already run, and nothing else. It decides by **content, not by number**: a file counts as applied only when its `--! Hash:` header (which step 2 kept) appears in `graphile_migrate.migrations`. Numbers aren't safe, because in a database shared with another app, that app's Graphile history has its own `000001.sql`.

It changes nothing and stops with an error when:

- a file's body no longer matches its own `--! Hash:` header, so it was edited after Graphile ran it;
- the matches have a gap, such as files 1, 2 and 4 matching but 3 not, so files were edited, renamed or reordered;
- the database has a Graphile migration after the last file that matched, so the files are behind the database;
- `SCHEMA` exists but no file matches, so the app's objects are there with no record of how they got there.

If the namespace already has applied migrations it does nothing, so running it twice is safe. If neither Graphile's history nor `SCHEMA` exists, it also does nothing, and `apply` builds the database from scratch.

Set the three values at the top for the database you're adopting, then run the block from the project root. It needs `node` (you have it for Graphile) and `psql`:

```bash
export DATABASE_URL=postgres://localhost/myapp   # a database Graphile migrated
NAMESPACE=default   # the -namespace you run mmmigrate with
SCHEMA=app          # a schema your migrations create, or "" if none

(
set -euo pipefail

# 1. Every Graphile file must still match its own --! Hash: header. Graphile
#    computes it from the previous hash and the body below the headers.
node -e '
const fs = require("fs"), crypto = require("crypto");
let bad = false;
for (const f of process.argv.slice(1)) {
  const lines = fs.readFileSync(f, "utf8").split("\n"), h = {};
  let i = 0;
  for (; i < lines.length; i++) {
    const m = /^--! ([A-Z][a-zA-Z0-9_]*)(?::(.*))?$/.exec(lines[i]);
    if (!m) break;
    h[m[1]] = m[2] ? m[2].trim() : "";
  }
  if (!h.Hash) continue; // written by mmmigrate, not Graphile
  const prev = h.Previous && h.Previous !== "-" ? h.Previous : "";
  const body = lines.slice(i).join("\n").trim() + "\n";
  const hash = "sha1:" + crypto.createHash("sha1")
    .update((prev + "\n" + body.trim()).trim() + "\n").digest("hex");
  if (hash !== h.Hash && !("AllowInvalidHash" in h) && !process.env.ALLOW_EDITED) {
    console.error(f + ": body does not match its --! Hash: header");
    bad = true;
  }
}
process.exit(bad ? 1 : 0);
' migrations/[0-9]*.sql

# 2. Create mmmigrate's tracking tables.
mmmigrate status -namespace "$NAMESPACE" >/dev/null

# 3. Record the files whose hash the database has, holding the same lock
#    as `mmmigrate apply` for this namespace.
{
  echo "CREATE TEMP TABLE adopt_files (version int PRIMARY KEY, name text NOT NULL, hash text, matched bool NOT NULL DEFAULT false);"
  for f in migrations/[0-9]*.sql; do
    b=$(basename "$f" .sql)
    h=$(sed -n 's/^--! Hash: *//p' "$f" | head -n1)
    hash=NULL; [ -n "$h" ] && hash="'$h'"
    printf "INSERT INTO adopt_files (version, name, hash) VALUES (%d, '%s', %s);\n" \
      "$((10#${b%%_*}))" "${b#*_}" "$hash"
  done
  cat <<'SQL'
CREATE TEMP TABLE adopt_params AS SELECT :'ns'::text AS ns, nullif(:'schema', '') AS schema;
DO $$
DECLARE
  p adopt_params;
  n int;
  last_version int;
  last_hash text;
  gap int;
  ahead text;
BEGIN
  SELECT * INTO p FROM adopt_params;
  -- The lock `mmmigrate apply` takes for this namespace, held until commit.
  PERFORM pg_advisory_xact_lock(1833701705, hashtext(p.ns));
  IF EXISTS (SELECT 1 FROM mmmigrate.applied WHERE namespace = p.ns) THEN
    RAISE NOTICE 'namespace % already has applied migrations; nothing to do', p.ns;
    RETURN;
  END IF;
  IF to_regclass('graphile_migrate.migrations') IS NOT NULL THEN
    EXECUTE 'UPDATE adopt_files f SET matched = true
             FROM graphile_migrate.migrations m WHERE m.hash = f.hash';
  END IF;
  SELECT count(*), max(version) INTO n, last_version FROM adopt_files WHERE matched;
  IF n = 0 THEN
    IF p.schema IS NOT NULL AND to_regnamespace(p.schema) IS NOT NULL THEN
      RAISE EXCEPTION 'schema % exists, but no migration file matches graphile_migrate.migrations: apply would replay over live data', p.schema;
    END IF;
    RAISE NOTICE 'no Graphile history for these files; nothing to adopt, apply will build from scratch';
    RETURN;
  END IF;
  SELECT min(version) INTO gap FROM adopt_files WHERE NOT matched AND version < last_version;
  IF gap IS NOT NULL THEN
    RAISE EXCEPTION 'migration % is not in graphile_migrate.migrations but later ones are: files were edited, renamed or reordered', gap;
  END IF;
  SELECT hash INTO last_hash FROM adopt_files WHERE version = last_version;
  EXECUTE 'SELECT filename FROM graphile_migrate.migrations WHERE previous_hash = $1'
    INTO ahead USING last_hash;
  IF ahead IS NOT NULL THEN
    RAISE EXCEPTION 'the database ran Graphile migration % after the last matching file (%): bring the files up to date first', ahead, last_version;
  END IF;
  INSERT INTO mmmigrate.applied (namespace, version, name)
    SELECT p.ns, version, name FROM adopt_files WHERE matched;
  RAISE NOTICE 'adopted % migrations (up to %) into namespace %', n, last_version, p.ns;
END $$;
SQL
} | psql "$DATABASE_URL" -X -q -v ON_ERROR_STOP=1 --single-transaction \
      -v ns="$NAMESPACE" -v schema="$SCHEMA"
)
```

Expect a `NOTICE` line saying what it did. The first time, mmmigrate also logs an `INFO` line as it creates its own tables. If it stops on the body check because you edited a migration on purpose (for example to hard-code a placeholder in step 4), review that diff, then rerun the block with `ALLOW_EDITED=1` set. Any other error means the database and the files disagree: resolve that before running `apply` against this database.

**Check:** `mmmigrate apply -dry-run -namespace "$NAMESPACE"` must list only migrations you wrote after the port, which is none right after porting. If it lists one this database already has, stop: don't `apply`.

Repeat for every database Graphile migrated: production, staging, and each developer's local copy. A brand-new database needs none of this, because `apply` builds it from nothing.

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
