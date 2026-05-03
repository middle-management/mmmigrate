# WASM build (pglite + sqlite-wasm)

mmmigrate compiles to WebAssembly and runs migrations against either
[pglite](https://github.com/electric-sql/pglite) (PostgreSQL) or
[`@sqlite.org/sqlite-wasm`](https://github.com/sqlite/sqlite-wasm)
(SQLite) — entirely inside a JS host. Use it to ship a self-contained
database into the browser, run integration tests in CI without spinning up
a database server, or build an interactive playground for migration
authoring.

The wasm artifact, JS glue, Node smoke test, and a runnable browser
playground live in
[`cmd/mmmigrate-wasm/`](https://github.com/middle-management/mmmigrate/tree/main/cmd/mmmigrate-wasm).

## How it works

The core engine reads migrations through `io/fs.FS` (see
[Library API](library.md)) and talks to the database through `database/sql`.
The WASM build adds three small adapters:

1. **`driver/jsdb`** — a `database/sql/driver` adapter that bridges to a
   JS object exposing `.exec(sql)` / `.query(sql, params)` returning
   Promises. The Go side awaits each Promise on the JS event loop while
   the calling goroutine yields. PGlite already speaks this contract;
   sqlite-wasm gets a thin wrapper.
2. **Dialects.** `driver/pglite` and `driver/sqlitejs` ship just a
   `Dialect` struct with the right SQL strings (advisory locks become
   no-ops for both — the wasm is single-process).
3. **`cmd/mmmigrate-wasm/jsfs`** — an `fs.FS` implementation that
   delegates to a JS-supplied `{ readDir, readFile }` adapter. Three
   ready-made adapters ship with the JS glue: in-memory map, OPFS, and
   Node `fs`.

Together they let the engine run unmodified inside the wasm.

## Build

```bash
cd cmd/mmmigrate-wasm
GOOS=js GOARCH=wasm go build -o mmmigrate.wasm .
```

You'll also need `wasm_exec.js` (Go's wasm runtime shim), bundled
alongside the source for convenience and also available at
`$(go env GOROOT)/lib/wasm/wasm_exec.js`.

## API

The wasm exposes two functions on the global `mmmigrate` namespace:

```ts
mmmigrate.apply(jsDb, options): Promise<{ ok: true }>
mmmigrate.render({ fs }):       Promise<string>
```

`jsDb` is any object with `.exec(sql)` and `.query(sql, params)` returning
Promises with the pglite-shaped result `{ rows, fields, affectedRows }`.

`options`:

| Field | Type | Default | Meaning |
|---|---|---|---|
| `fs` | `{ readDir, readFile }` | required | filesystem adapter |
| `dialect` | `'postgres' \| 'sqlite'` | `'postgres'` | which `Dialect` to use |
| `applyCurrent` | `boolean` | `true` | also re-run `current.sql` |

`render` returns the expanded `current.sql` (with `@include` directives
inlined) — useful for previewing what `apply` would actually execute.

## Use it from JS

=== "pglite (PostgreSQL)"

    ```js
    import { loadMmmigrate, fs as mmfs } from './glue/mmmigrate.mjs'
    import { PGlite } from '@electric-sql/pglite'

    const mm = await loadMmmigrate(fetch('./mmmigrate.wasm'))
    const db = new PGlite()
    await db.waitReady

    await mm.apply(db, {
      fs: mmfs.fromMap({
        '001_users.sql': 'CREATE TABLE users(id INT PRIMARY KEY)',
        'current.sql':   '-- re-run on every apply',
      }),
      dialect: 'postgres',
      applyCurrent: true,
    })
    ```

=== "sqlite-wasm (SQLite)"

    ```js
    import { loadMmmigrate, fs as mmfs, db as mmdb } from './glue/mmmigrate.mjs'
    import sqlite3InitModule from '@sqlite.org/sqlite-wasm'

    const mm = await loadMmmigrate(fetch('./mmmigrate.wasm'))
    const sqlite3 = await sqlite3InitModule()
    const sqliteDB = new sqlite3.oo1.DB(':memory:')

    await mm.apply(mmdb.adaptSQLiteWasm(sqliteDB), {
      fs: mmfs.fromMap({
        '001_users.sql': 'CREATE TABLE users(id INTEGER PRIMARY KEY)',
        'current.sql':   '-- re-run on every apply',
      }),
      dialect: 'sqlite',
      applyCurrent: true,
    })
    ```

`mmdb.adaptSQLiteWasm(sqliteOO1DB)` wraps an `oo1.DB` so it satisfies the
pglite-shaped contract. The wasm doesn't care which engine it's talking to
beyond the dialect flag.

## FS adapters

| Adapter | When to use |
|---|---|
| `mmfs.fromMap(obj)` | Inline migrations as `{ filename: SQL }`. |
| `mmfs.fromOPFS(handle)` | Browser: migrations stored in [OPFS](https://developer.mozilla.org/en-US/docs/Web/API/File_System_API/Origin_private_file_system) — persisted across reloads. |
| `mmfs.fromNodeFS(dir)` | Node: migrations on disk, like the CLI. |

You can also write your own — anything shaped like
`{ readDir(p): Promise<{name,isDir}[]>, readFile(p): Promise<Uint8Array> }`
will work.

## Browser playground

The repository includes a static playground at
[`cmd/mmmigrate-wasm/example/`](https://github.com/middle-management/mmmigrate/tree/main/cmd/mmmigrate-wasm/example):

- **Engine dropdown** — switch between Postgres (pglite) and SQLite
  (sqlite-wasm). Each engine has its own seed migrations and a separate
  OPFS namespace.
- **CodeMirror 6 editor** with SQL syntax highlighting (per dialect).
- **Inline tab** — files in memory; the file picker on the left selects
  what's open in the editor.
- **OPFS tab** — files in
  `OPFS:/mmmigrate-example/<engine>/migrations/`, persisted across
  reloads. Includes a small file explorer (add/edit/delete/seed-reset).
  For richer OPFS inspection during development, install the
  [OPFS Explorer](https://chromewebstore.google.com/detail/opfs-explorer/acndjpgkpaclldomagafnognkcgjignd)
  Chrome DevTools extension.
- **`@include` badges** — when editing `current.sql`, parsed include paths
  are shown as clickable badges that switch the editor to the included
  file.
- **Render preview** — calls `mmmigrate.render` and shows the expanded
  `current.sql` so you can see exactly what would run before you Apply.

Run it locally:

```bash
cd cmd/mmmigrate-wasm
GOOS=js GOARCH=wasm go build -o mmmigrate.wasm .
python3 -m http.server 8000
# then open http://localhost:8000/example/
```

OPFS requires a secure context — `localhost` qualifies.

## Limitations

- Only `apply` and `render` are exposed. `commit`, `revert`, `status`,
  `validate`, `watch`, and `init` need a writable FS surface (and in the
  case of `commit`, more API design); they're CLI-only for now.
- The migration lock is a no-op (single-process wasm).
- SQLite playground is in-memory only; OPFS-persistent SQLite needs a
  Worker (SAH file handles), which is a larger lift. The OPFS *migration
  source* still works for both dialects.
- Build size is ~5 MB uncompressed (~1 MB gzipped) for `mmmigrate.wasm`;
  load it lazily on pages that need it.
