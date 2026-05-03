# WASM build (pglite)

mmmigrate compiles to WebAssembly and runs migrations against
[pglite](https://github.com/electric-sql/pglite) — a WASM build of
PostgreSQL — entirely inside a JS host. Use it to ship a self-contained
database into the browser, run integration tests in CI without spinning up
a Postgres server, or seed an SPA's local state.

The wasm artifact, JS glue, and a runnable browser example live in
[`cmd/mmmigrate-wasm/`](https://github.com/middle-management/mmmigrate/tree/main/cmd/mmmigrate-wasm).

## How it works

The core engine reads migrations through `io/fs.FS` (see [Library API](library.md))
and talks to the database through `database/sql`. The WASM build adds two
small adapters:

1. **`driver/pglite`** — a `database/sql/driver` adapter that bridges to
   pglite's JS API via `syscall/js`. Each Go database call awaits a JS
   Promise on the JS event loop while the calling goroutine yields. The
   pglite Dialect is identical to the PostgreSQL dialect except advisory
   locks are no-ops (pglite is single-process).
2. **`cmd/mmmigrate-wasm/jsfs`** — an `fs.FS` implementation that delegates
   to a JS-supplied `{ readDir, readFile }` adapter. Three ready-made
   adapters ship with the JS glue: in-memory map, OPFS, and Node `fs`.

Together they let the engine run unmodified inside the wasm.

## Build

```bash
cd cmd/mmmigrate-wasm
GOOS=js GOARCH=wasm go build -o mmmigrate.wasm .
```

You'll also need `wasm_exec.js` (Go's wasm runtime shim), which is bundled
alongside the source for convenience and is also at
`$(go env GOROOT)/lib/wasm/wasm_exec.js`.

## Use it from JS

```js
import { loadMmmigrate, fs as mmfs } from './glue/mmmigrate.mjs'
import { PGlite } from '@electric-sql/pglite'

const mm = await loadMmmigrate(fetch('./mmmigrate.wasm'))
const db = new PGlite()
await db.waitReady

await mm.apply(db, {
  fs: mmfs.fromMap({
    '001_users.sql': 'CREATE TABLE users(id INT PRIMARY KEY, name TEXT)',
    'current.sql':   '-- re-run on every apply',
  }),
  applyCurrent: true,
})
```

## FS adapters

| Adapter | When to use |
|---|---|
| `mmfs.fromMap(obj)` | Inline migrations as `{ filename: SQL }`. |
| `mmfs.fromOPFS(handle)` | Browser: migrations stored in [OPFS](https://developer.mozilla.org/en-US/docs/Web/API/File_System_API/Origin_private_file_system) — persisted across reloads. |
| `mmfs.fromNodeFS(dir)` | Node: migrations on disk, like the CLI. |

You can also write your own — anything shaped like
`{ readDir(p): Promise<{name,isDir}[]>, readFile(p): Promise<Uint8Array> }`
will work. This is how the example page wires migrations from a UI form.

## Browser example

The repository includes a static example at
[`cmd/mmmigrate-wasm/example/`](https://github.com/middle-management/mmmigrate/tree/main/cmd/mmmigrate-wasm/example)
with two tabs:

- **Inline** — edit migration SQL in textareas and apply against a fresh
  pglite instance.
- **OPFS** — migrations live in
  `navigator.storage:/mmmigrate-example/migrations/`. The tab embeds a
  small OPFS file explorer (inspired by
  [opfs-explorer](https://github.com/mickaelvieira/opfs-explorer)) for
  adding, editing, and deleting migration files in place. Edits persist
  across page reloads.

Run it locally:

```bash
cd cmd/mmmigrate-wasm
GOOS=js GOARCH=wasm go build -o mmmigrate.wasm .
python3 -m http.server 8000
# then open http://localhost:8000/example/
```

OPFS requires a secure context — `localhost` qualifies.

## Limitations

- Only `apply` is exposed. `status`, `validate`, `commit`, `revert`,
  `watch`, `init`, and `render` are CLI-only.
- The migration lock is a no-op; pglite is single-process so concurrent
  runs aren't possible.
- Build size is ~5 MB uncompressed (~1 MB gzipped) — load it lazily on
  pages that need it.
