// Node smoke test for mmmigrate-wasm.
//
// Builds the wasm out-of-band (see ../README.md) and exercises the apply()
// + render() entrypoints against an in-memory pglite instance and an
// in-memory @sqlite.org/sqlite-wasm instance. Run with:
//
//   npm install
//   node run.mjs

import assert from "node:assert/strict"
import { readFile } from "node:fs/promises"
import { fileURLToPath } from "node:url"
import { dirname, join } from "node:path"

import { createRequire } from "node:module"
import "../wasm_exec.js"
import { PGlite } from "@electric-sql/pglite"
import { loadMmmigrate, fs as mmfs, db as mmdb } from "../glue/mmmigrate.mjs"

// In Node, the default sqlite-wasm entry references `self` which isn't
// defined. The bundled Node-compatible entry isn't reachable via package
// exports either, so resolve it manually through node_modules.
const requireCJS = createRequire(import.meta.url)
const sqliteWasmPkgPath = requireCJS.resolve("@sqlite.org/sqlite-wasm/package.json")
const sqliteWasmDir = dirname(sqliteWasmPkgPath)
const { default: sqlite3InitModule } = await import(
  join(sqliteWasmDir, "sqlite-wasm", "jswasm", "sqlite3-node.mjs")
)

const here = dirname(fileURLToPath(import.meta.url))
const wasmPath = join(here, "..", "mmmigrate.wasm")

const PG_MIGRATIONS = {
  "001_users.sql":
    "CREATE TABLE users (id INT PRIMARY KEY, name TEXT);",
  "002_posts.sql":
    "CREATE TABLE posts (id INT PRIMARY KEY, user_id INT REFERENCES users(id), body TEXT);",
  "current.sql": "ALTER TABLE users ADD COLUMN IF NOT EXISTS bio TEXT;",
}

const SQLITE_MIGRATIONS = {
  "001_users.sql": "CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT);",
  "002_posts.sql":
    "CREATE TABLE posts (id INTEGER PRIMARY KEY, user_id INTEGER REFERENCES users(id), body TEXT);",
  "current.sql":
    "CREATE TABLE IF NOT EXISTS notes (id INTEGER PRIMARY KEY, body TEXT);",
}

async function testPGlite(mm) {
  console.log("\n=== pglite ===")
  const db = new PGlite()
  await db.waitReady

  console.log("→ apply()")
  await mm.apply(db, {
    fs: mmfs.fromMap(PG_MIGRATIONS),
    dialect: "postgres",
    applyCurrent: true,
  })

  const applied = await db.query(
    "SELECT version, name FROM mmmigrate.applied ORDER BY version",
  )
  assert.equal(applied.rows.length, 2)
  assert.equal(applied.rows[0].name, "users")
  assert.equal(applied.rows[1].name, "posts")

  const cols = await db.query(
    `SELECT column_name FROM information_schema.columns
       WHERE table_name = 'users' ORDER BY column_name`,
  )
  assert.deepEqual(
    cols.rows.map((r) => r.column_name),
    ["bio", "id", "name"],
  )

  console.log("→ apply() (re-run, should be no-op)")
  await mm.apply(db, {
    fs: mmfs.fromMap(PG_MIGRATIONS),
    dialect: "postgres",
    applyCurrent: true,
  })
  const n = await db.query("SELECT count(*)::int AS n FROM mmmigrate.applied")
  assert.equal(n.rows[0].n, 2)

  console.log("→ render()")
  const rendered = await mm.render({ fs: mmfs.fromMap(PG_MIGRATIONS) })
  assert.match(rendered, /ALTER TABLE users ADD COLUMN/)

  console.log("✓ pglite passed")
}

async function testSQLite(mm) {
  console.log("\n=== sqlite-wasm ===")
  const sqlite3 = await sqlite3InitModule()
  const sqliteDB = new sqlite3.oo1.DB(":memory:")
  const db = mmdb.adaptSQLiteWasm(sqliteDB)

  console.log("→ apply()")
  await mm.apply(db, {
    fs: mmfs.fromMap(SQLITE_MIGRATIONS),
    dialect: "sqlite",
    applyCurrent: true,
  })

  const applied = await db.query(
    "SELECT version, name FROM mmmigrate_applied ORDER BY version",
  )
  assert.equal(applied.rows.length, 2)
  assert.equal(applied.rows[0].name, "users")
  assert.equal(applied.rows[1].name, "posts")

  const tables = await db.query(
    "SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name",
  )
  const tableNames = tables.rows.map((r) => r.name).sort()
  for (const expected of ["mmmigrate_applied", "mmmigrate_current", "notes", "posts", "users"]) {
    assert.ok(tableNames.includes(expected), `expected table ${expected}, got ${tableNames}`)
  }

  console.log("→ apply() (re-run, should be no-op)")
  await mm.apply(db, {
    fs: mmfs.fromMap(SQLITE_MIGRATIONS),
    dialect: "sqlite",
    applyCurrent: true,
  })
  const n = await db.query("SELECT count(*) AS n FROM mmmigrate_applied")
  assert.equal(n.rows[0].n, 2)

  console.log("✓ sqlite-wasm passed")
}

async function main() {
  const wasmBytes = await readFile(wasmPath)
  const mm = await loadMmmigrate(wasmBytes)

  await testPGlite(mm)
  await testSQLite(mm)

  console.log("\n✓ all assertions passed")
  process.exit(0)
}

main().catch((err) => {
  console.error("✗ test failed:", err)
  process.exit(1)
})
