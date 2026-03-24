# mmmigrate

File-based SQL migration tool for PostgreSQL and SQLite.

## Skills

Project skills live in `skills/` and are symlinked into `~/.claude/skills/` for discovery. After cloning, run:

```bash
ln -sf "$(pwd)/skills/mmmigrate" ~/.claude/skills/mmmigrate
```

## Building

Requires a driver build tag:

```bash
go build -tags sqlite .
go build -tags postgres .
```

## Testing

```bash
go test ./source/...                                          # unit tests (no DB)
cd driver/sqlite && go test ./...                             # sqlite integration
cd driver/postgres && MMMIGRATE_TEST_POSTGRES_URL=... go test # postgres integration
```

Update golden files with `MMMIGRATE_UPDATE_GOLDEN=1`.

## Module structure

- Root module — CLI binary, `migrate/` (engine), `source/` (filesystem ops)
- `driver/postgres/` — separate module, PostgreSQL dialect + pgx driver
- `driver/sqlite/` — separate module, SQLite dialect + modernc driver
