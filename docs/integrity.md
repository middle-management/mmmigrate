# Integrity

Every committed migration carries two hashes: a content **checksum** and a chain hash that links it to all previous migrations. Together they form a merkle chain that detects tampering — accidental edits, intentional rewrites, or accidentally inserting a migration out of order.

## What's stored

Each committed migration begins with header comments:

```sql
-- Migration: create users table
-- Created: 2026-05-03T12:00:00Z
-- Checksum: a1b2c3...
-- Chain: d4e5f6...
--
-- IMPORTANT: Do not modify this file after commit.
--
CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT NOT NULL);
```

| Field | What it is |
|------|----------|
| `Checksum` | SHA-256 of the migration body (everything after the blank line that ends the header) — the compiled `current.sql`, including any comments it starts with |
| `Chain` | SHA-256 of `previous_chain || current_checksum` |

The first migration's chain is just the SHA-256 of its checksum. Each subsequent migration folds the previous chain into its own, so any earlier modification cascades and invalidates every later migration.

## Verification

`mmmigrate validate` recomputes both hashes for every committed migration and compares them to the stored values:

```bash
mmmigrate validate
```

It fails if:

- Two migrations share a version.
- Any migration body has changed since commit (checksum mismatch).
- Any migration was renumbered, deleted, or inserted out of order (chain mismatch).
- The chain links don't match the stored values.

## Two branches, one version

The usual way two migrations end up with one version is two branches that each run `commit` off the same main: both take the next number, both chain after the same predecessor, and their files have different names, so git merges them without a conflict. `apply` then refuses to load the directory (`duplicate migration version N`), and every deployment of the merged code fails to start.

`validate` reports it as such:

```
duplicate migration version 30: 030_add_tags.sql and 030_batch_draws.sql (renumber the later one and recompute its chain)
```

Each branch's own CI passed, since each branch alone is valid. To catch it before merge, `validate` has to run on the merge result: enable "require branches to be up to date before merging" or a merge queue in the repository's settings, and run `validate` (or a test that calls `source.ValidateChain`) in CI on `pull_request` and, with a merge queue, `merge_group` events.

To fix it, renumber the migration that merged second: rename the file to the next free version and rewrite its `Chain:` header to chain after its new predecessor (its body and `Checksum:` stay the same). A database that already applied it under the old number has it recorded there, and `apply`, which matches by version, would take it for the other migration; re-record it under its new version before deploying, e.g. on PostgreSQL

```sql
UPDATE mmmigrate.applied SET version = 31
WHERE namespace = 'default' AND version = 30 AND name = 'batch_draws';
```

and the next `apply` runs the migration it never had.

## CI gate

A typical CI workflow runs both `check` and `validate`:

```bash
mmmigrate check       # current.sql is empty
mmmigrate validate    # committed migrations are intact
```

`check` ensures no developer is shipping a branch with un-committed schema changes; `validate` ensures nobody edited a committed file by hand.

## What it does NOT do

- It does **not** verify that the database state matches the migration chain. Use `mmmigrate status` against your production DB for that.
- It does **not** prevent you from editing committed files — it only detects after the fact. Treat committed migration files as immutable: add `* linguist-generated=true` to `.gitattributes` if you want them excluded from PR diffs.
- It does **not** sign migrations cryptographically. The chain protects against accidental edits and reordering, not against an attacker who can also rewrite the chain hashes.
