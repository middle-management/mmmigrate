# Includes

Most schemas have SQL that you'd like to maintain in one place — trigger functions, views, type definitions, common indexes — and reuse across migrations. mmmigrate's `@include` directive does exactly that.

## Syntax

Include other SQL files from `current.sql` (or from any draft):

```sql
-- migrations/current.sql
CREATE TABLE events (
    id SERIAL PRIMARY KEY,
    name TEXT NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- @include functions/notify_event.sql
-- @include views/recent_events.sql
```

The included files live in subdirectories under `migrations/`:

```
migrations/
├── current.sql
├── functions/
│   └── notify_event.sql
└── views/
    └── recent_events.sql
```

Includes can themselves contain `@include` directives, but cycles are rejected.

## Path restrictions

Include paths are **always relative to the migrations directory**, and they cannot escape it. `../etc/passwd`, absolute paths, or symlinks pointing outside the migrations tree are rejected. This is a safety guard, not a configuration option.

## Behavior on commit

When you run `mmmigrate commit`, all `@include` directives in `current.sql` are expanded inline so the committed migration file is fully self-contained:

```sql
-- migrations/001_add_events.sql
-- Migration: add events table
-- Created: 2026-05-03T12:00:00Z
-- Checksum: a1b2c3...
-- Chain: d4e5f6...
--
-- IMPORTANT: Do not modify this file after commit.
--
CREATE TABLE events (
    id SERIAL PRIMARY KEY,
    name TEXT NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- BEGIN INCLUDE functions/notify_event.sql
CREATE OR REPLACE FUNCTION notify_event() ...
-- END INCLUDE functions/notify_event.sql

-- BEGIN INCLUDE views/recent_events.sql
CREATE OR REPLACE VIEW recent_events AS ...
-- END INCLUDE views/recent_events.sql
```

The `BEGIN INCLUDE` / `END INCLUDE` markers are how `revert` later restores the original `@include` directives.

## Behavior on revert

`mmmigrate revert` is the inverse: it moves the committed migration back into `current.sql`, replacing each `BEGIN INCLUDE` / `END INCLUDE` block with the original `@include` directive. The included file content is left untouched in its subdirectory.

## Tips

- **Keep includes idempotent.** Just like `current.sql`, an included file may run multiple times during development. Use `CREATE OR REPLACE` for functions and views, `DROP TRIGGER IF EXISTS ... CREATE TRIGGER` for triggers.
- **Edit included files freely while drafting.** Watch mode picks up changes to includes the same way it picks up changes to `current.sql`.
- **Don't share state across migrations via includes.** Once a migration is committed, the included content is frozen inline. Editing the include file later only affects future commits — old migrations keep their snapshot.
