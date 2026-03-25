# mmmigrate

File-based SQL migration tool for PostgreSQL, SQLite, and MySQL.

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
```

## Testing

```bash
go test ./...                                                 # library + unit tests (no DB)
cd driver/sqlite && go test ./...                             # sqlite integration
cd driver/postgres && MMMIGRATE_TEST_POSTGRES_URL=... go test # postgres integration
cd driver/mysql && MMMIGRATE_TEST_MYSQL_URL=... go test       # mysql integration
```

Update golden files with `MMMIGRATE_UPDATE_GOLDEN=1`.

## Module structure

- Root module — library: `mmmigrate` package (engine, Dialect interface), `source/` (filesystem ops), `migratetest/` (shared test harness)
- `cmd/mmmigrate/` — separate module, CLI binary with build-tag driver selection (all driver deps live here)
- `driver/postgres/` — separate module, PostgreSQL dialect + pgx driver
- `driver/sqlite/` — separate module, SQLite dialect + modernc driver
- `driver/mysql/` — separate module, MySQL dialect + go-sql-driver
