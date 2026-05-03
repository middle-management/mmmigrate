// Node smoke test for mmmigrate-wasm.
//
// Builds the wasm out-of-band (see ../README.md) and exercises the apply()
// entrypoint against an in-memory pglite instance using the fromMap fs
// adapter. Run with:  node test/run.mjs
//
// Prerequisites:  npm install --no-save @electric-sql/pglite

import assert from "node:assert/strict"
import { readFile } from "node:fs/promises"
import { fileURLToPath } from "node:url"
import { dirname, join } from "node:path"

import "../wasm_exec.js"
import { PGlite } from "@electric-sql/pglite"
import { loadMmmigrate, fs as mmfs } from "../glue/mmmigrate.mjs"

const here = dirname(fileURLToPath(import.meta.url))
const wasmPath = join(here, "..", "mmmigrate.wasm")

async function main() {
  const wasmBytes = await readFile(wasmPath)
  const mm = await loadMmmigrate(wasmBytes)

  const db = new PGlite()
  await db.waitReady

  const migrations = mmfs.fromMap({
    "001_users.sql": "CREATE TABLE users (id INT PRIMARY KEY, name TEXT);",
    "002_posts.sql":
      "CREATE TABLE posts (id INT PRIMARY KEY, user_id INT REFERENCES users(id), body TEXT);",
    "current.sql":
      "ALTER TABLE users ADD COLUMN IF NOT EXISTS bio TEXT;",
  })

  console.log("→ apply()")
  await mm.apply(db, { fs: migrations, applyCurrent: true })

  // Verify mmmigrate.applied has both numbered migrations.
  const applied = await db.query(
    "SELECT version, name FROM mmmigrate.applied ORDER BY version",
  )
  assert.equal(applied.rows.length, 2, "expected 2 applied migrations")
  assert.equal(applied.rows[0].version, 1)
  assert.equal(applied.rows[0].name, "users")
  assert.equal(applied.rows[1].version, 2)
  assert.equal(applied.rows[1].name, "posts")

  // Verify users table has the bio column from current.sql.
  const cols = await db.query(
    `SELECT column_name FROM information_schema.columns
       WHERE table_name = 'users' ORDER BY column_name`,
  )
  const names = cols.rows.map((r) => r.column_name)
  assert.deepEqual(names, ["bio", "id", "name"])

  // Re-apply should be a no-op.
  console.log("→ apply() (re-run, should be a no-op)")
  await mm.apply(db, { fs: migrations, applyCurrent: true })
  const applied2 = await db.query(
    "SELECT count(*)::int AS n FROM mmmigrate.applied",
  )
  assert.equal(applied2.rows[0].n, 2)

  console.log("✓ all assertions passed")
  process.exit(0)
}

main().catch((err) => {
  console.error("✗ test failed:", err)
  process.exit(1)
})
