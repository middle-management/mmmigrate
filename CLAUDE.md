# mmmigrate

File-based SQL migration tool for PostgreSQL, SQLite, MySQL, and Spanner.

## Skills

Project skills live in `skills/` and are symlinked into `~/.claude/skills/` for discovery. After cloning, run:

```bash
ln -sf "$(pwd)/skills/mmmigrate" ~/.claude/skills/mmmigrate
```

## Building

The CLI lives in `cmd/mmmigrate/` and requires a driver build tag:

```bash
cd cmd/mmmigrate
go build -tags sqlite .
go build -tags postgres .
go build -tags mysql .
go build -tags spanner .
```

## Testing

```bash
go test ./...                                                 # library + unit tests (no DB)
cd driver/sqlite && go test ./...                             # sqlite integration
cd driver/postgres && MMMIGRATE_TEST_POSTGRES_URL=... go test # postgres integration
cd driver/mysql && MMMIGRATE_TEST_MYSQL_URL=... go test       # mysql integration
cd driver/spanner && MMMIGRATE_TEST_SPANNER_URL=... go test   # spanner integration (PGAdapter emulator)
cd driver/spanner/gosqlspannertest && MMMIGRATE_TEST_SPANNER_EMULATOR_HOST=localhost:9010 go test   # same suite via go-sql-spanner
```

Update golden files with `MMMIGRATE_UPDATE_GOLDEN=1`.

## Changing the tracking tables

Never edit a dialect's `CreateMigrationsTable` or an existing file in `selfmigrations/`. Write the change in `selfmigrations/{postgres,sqlite,mysql,spanner}/current.sql` and commit each with `mmmigrate commit -migrations selfmigrations/<flavour> -description "..." -skip-verify`. Existing databases pick it up on their next run.

## Module structure

- Root module — library: `mmmigrate` package (engine, Dialect interface), `source/` (filesystem ops), `migratetest/` (shared test harness), `selfmigrations/` (migrations for mmmigrate's own tracking tables, per SQL flavour)
- `cmd/mmmigrate/` — separate module, CLI binary with build-tag driver selection (all driver deps live here)
- `driver/postgres/` — separate module, PostgreSQL dialect + pgx driver
- `driver/sqlite/` — separate module, SQLite dialect + modernc driver
- `driver/mysql/` — separate module, MySQL dialect + go-sql-driver
- `driver/spanner/` — separate module, Spanner (PostgreSQL dialect) Dialect, no driver deps
- `driver/spanner/gosqlspannertest/` — test-only module running the suite via go-sql-spanner
