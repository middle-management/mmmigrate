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
| `Checksum` | SHA-256 of the migration body (everything after the header) |
| `Chain` | SHA-256 of `previous_chain || current_checksum` |

The first migration's chain is just the SHA-256 of its checksum. Each subsequent migration folds the previous chain into its own, so any earlier modification cascades and invalidates every later migration.

## Verification

`mmmigrate validate` recomputes both hashes for every committed migration and compares them to the stored values:

```bash
mmmigrate validate
```

It fails if:

- Any migration body has changed since commit (checksum mismatch).
- Any migration was renumbered, deleted, or inserted out of order (chain mismatch).
- The chain links don't match the stored values.

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
