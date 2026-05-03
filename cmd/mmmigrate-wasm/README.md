# mmmigrate-wasm

WebAssembly build of mmmigrate that runs against
[pglite](https://github.com/electric-sql/pglite) — a WASM build of
PostgreSQL — entirely inside a JS host (browser or Node).

## Build

```bash
cd cmd/mmmigrate-wasm
GOOS=js GOARCH=wasm go build -o mmmigrate.wasm .
```

`mmmigrate.wasm` is the only artifact you need to ship alongside the JS
glue (`glue/mmmigrate.mjs`) and the Go runtime shim (`wasm_exec.js`,
copied here from `$(go env GOROOT)/lib/wasm/`).

## API

```js
import { loadMmmigrate, fs as mmfs } from './glue/mmmigrate.mjs'

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

Three FS adapters ship with the glue:

| Adapter | When to use |
|---|---|
| `mmfs.fromMap(obj)` | Inline migrations as a `{ filename: SQL }` object. |
| `mmfs.fromOPFS(handle)` | Browser: migrations stored in OPFS, persisted across reloads. |
| `mmfs.fromNodeFS(dir)` | Node: migrations on disk, like the CLI. |

You can also supply your own adapter — anything with
`{ readDir(path): Promise<{name,isDir}[]>, readFile(path): Promise<Uint8Array> }`
will work.

## Node smoke test

```bash
cd test
npm install            # installs @electric-sql/pglite
node run.mjs           # runs migrations + asserts schema
```

## Browser example

```bash
# from cmd/mmmigrate-wasm/
python3 -m http.server 8000
# open http://localhost:8000/example/
```

The example has two tabs:

- **Inline** — edit migration SQL in textareas and apply against a fresh
  pglite instance.
- **OPFS** — migrations live in
  `navigator.storage:/mmmigrate-example/migrations/`. Includes a tiny OPFS
  file explorer (inspired by [opfs-explorer]) so you can add / rename /
  edit files; edits persist across reloads.

[opfs-explorer]: https://github.com/mickaelvieira/opfs-explorer

OPFS requires a secure context (`localhost` qualifies).

## Limitations

- `apply` only — `status`, `validate`, `commit`, `revert`, `watch`, `init`,
  and `render` are not exposed via wasm.
- Single pglite instance per `apply` call. The migration lock is a no-op
  (pglite is single-process).
