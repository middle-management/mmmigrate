import { PGlite } from "https://cdn.jsdelivr.net/npm/@electric-sql/pglite@0.2.17/dist/index.js"
import { loadMmmigrate, fs as mmfs } from "../glue/mmmigrate.mjs"

// ── boot wasm ─────────────────────────────────────────────────────────

const mm = await loadMmmigrate(fetch("../mmmigrate.wasm"))

// ── tabs ──────────────────────────────────────────────────────────────

document.querySelectorAll(".tabs button").forEach((btn) => {
  btn.addEventListener("click", () => {
    document.querySelectorAll(".tabs button").forEach((b) => b.classList.remove("active"))
    document.querySelectorAll(".tab").forEach((t) => t.classList.remove("active"))
    btn.classList.add("active")
    document.getElementById("tab-" + btn.dataset.tab).classList.add("active")
  })
})

// ── shared seed migrations ────────────────────────────────────────────

const SEED_MIGRATIONS = {
  "001_users.sql":
    "CREATE TABLE users (\n  id   INT PRIMARY KEY,\n  name TEXT NOT NULL\n);\n",
  "002_posts.sql":
    "CREATE TABLE posts (\n  id      INT PRIMARY KEY,\n  user_id INT REFERENCES users(id),\n  body    TEXT\n);\n",
  "current.sql":
    "-- current.sql is re-run on every apply.\nALTER TABLE users ADD COLUMN IF NOT EXISTS bio TEXT;\n",
}

async function reportSchema(db, out) {
  const applied = await db.query(
    "SELECT version, name, applied_at FROM mmmigrate.applied ORDER BY version",
  )
  const tables = await db.query(
    `SELECT table_name FROM information_schema.tables
       WHERE table_schema = 'public' ORDER BY table_name`,
  )
  out.textContent =
    "✓ apply succeeded\n\n" +
    "mmmigrate.applied:\n" +
    applied.rows
      .map((r) => `  ${String(r.version).padStart(3, "0")}  ${r.name}  (${r.applied_at})`)
      .join("\n") +
    "\n\npublic tables:\n" +
    tables.rows.map((r) => "  " + r.table_name).join("\n")
  out.dataset.status = "ok"
}

function showError(out, err) {
  out.textContent = "✗ " + (err?.stack || err?.message || String(err))
  out.dataset.status = "err"
}

// ── INLINE TAB ────────────────────────────────────────────────────────

const inlineEditor = document.getElementById("inline-editor")
const inlineOutput = document.getElementById("inline-output")
const inlineFiles = { ...SEED_MIGRATIONS }

function renderInlineEditor() {
  inlineEditor.innerHTML = ""
  for (const name of Object.keys(inlineFiles)) {
    const wrap = document.createElement("div")
    wrap.className = "file"
    const head = document.createElement("header")
    head.textContent = name
    const ta = document.createElement("textarea")
    ta.value = inlineFiles[name]
    ta.addEventListener("input", () => (inlineFiles[name] = ta.value))
    wrap.append(head, ta)
    inlineEditor.append(wrap)
  }
}
renderInlineEditor()

document.getElementById("inline-apply").addEventListener("click", async () => {
  inlineOutput.textContent = "running…"
  inlineOutput.dataset.status = "idle"
  try {
    const db = new PGlite()
    await db.waitReady
    await mm.apply(db, { fs: mmfs.fromMap(inlineFiles), applyCurrent: true })
    await reportSchema(db, inlineOutput)
  } catch (err) {
    showError(inlineOutput, err)
  }
})

// ── OPFS TAB ──────────────────────────────────────────────────────────
//
// The OPFS tab keeps migration files in
//   navigator.storage:/mmmigrate-example/migrations/
// so edits persist across reloads. The tree view is a minimal OPFS file
// explorer (inspired by mickaelvieira/opfs-explorer) — enough to add /
// rename / edit / delete files for demo purposes.

const opfsTreeList = document.getElementById("opfs-tree-list")
const opfsEditorArea = document.getElementById("opfs-editor-area")
const opfsCurrentPath = document.getElementById("opfs-current-path")
const opfsOutput = document.getElementById("opfs-output")

let opfsRoot = null            // FileSystemDirectoryHandle for migrations dir
let opfsSelectedPath = null    // forward-slash path relative to migrations root

async function getMigrationsDir({ resetSeed = false } = {}) {
  if (!navigator.storage?.getDirectory) {
    throw new Error("OPFS is not available in this browser")
  }
  const top = await navigator.storage.getDirectory()
  const ns = await top.getDirectoryHandle("mmmigrate-example", { create: true })
  const dir = await ns.getDirectoryHandle("migrations", { create: true })

  if (resetSeed) {
    for await (const name of (async function* () {
      for await (const k of dir.keys()) yield k
    })()) {
      await dir.removeEntry(name, { recursive: true })
    }
  }

  // Seed only if empty.
  let empty = true
  for await (const _ of dir.keys()) {
    empty = false
    break
  }
  if (empty) {
    for (const [name, body] of Object.entries(SEED_MIGRATIONS)) {
      const fh = await dir.getFileHandle(name, { create: true })
      const w = await fh.createWritable()
      await w.write(body)
      await w.close()
    }
  }
  return dir
}

async function listOpfsTree() {
  const items = []
  async function walk(dir, prefix) {
    const entries = []
    for await (const [name, handle] of dir.entries()) {
      entries.push({ name, handle })
    }
    entries.sort((a, b) => {
      const da = a.handle.kind === "directory" ? 0 : 1
      const db = b.handle.kind === "directory" ? 0 : 1
      return da - db || a.name.localeCompare(b.name)
    })
    for (const { name, handle } of entries) {
      const path = prefix ? `${prefix}/${name}` : name
      const isDir = handle.kind === "directory"
      items.push({ path, name, isDir, depth: prefix ? prefix.split("/").length : 0 })
      if (isDir) await walk(handle, path)
    }
  }
  await walk(opfsRoot, "")
  return items
}

async function renderOpfsTree() {
  const items = await listOpfsTree()
  opfsTreeList.innerHTML = ""
  for (const it of items) {
    const li = document.createElement("li")
    li.textContent = "  ".repeat(it.depth) + it.name
    li.className = it.isDir ? "dir" : "file"
    li.dataset.path = it.path
    li.dataset.kind = it.isDir ? "dir" : "file"
    if (it.path === opfsSelectedPath) li.classList.add("selected")
    li.addEventListener("click", () => selectOpfsEntry(it))
    opfsTreeList.append(li)
  }
}

async function readOpfsFile(path) {
  const parts = path.split("/")
  const name = parts.pop()
  let dir = opfsRoot
  for (const p of parts) dir = await dir.getDirectoryHandle(p)
  const fh = await dir.getFileHandle(name)
  return await (await fh.getFile()).text()
}

async function writeOpfsFile(path, body) {
  const parts = path.split("/")
  const name = parts.pop()
  let dir = opfsRoot
  for (const p of parts) dir = await dir.getDirectoryHandle(p, { create: true })
  const fh = await dir.getFileHandle(name, { create: true })
  const w = await fh.createWritable()
  await w.write(body)
  await w.close()
}

async function deleteOpfsEntry(path) {
  const parts = path.split("/")
  const name = parts.pop()
  let dir = opfsRoot
  for (const p of parts) dir = await dir.getDirectoryHandle(p)
  await dir.removeEntry(name, { recursive: true })
}

async function selectOpfsEntry(it) {
  opfsSelectedPath = it.path
  await renderOpfsTree()
  if (it.isDir) {
    opfsCurrentPath.textContent = `${it.path}/  (directory)`
    opfsEditorArea.value = ""
    opfsEditorArea.disabled = true
  } else {
    opfsEditorArea.disabled = false
    opfsEditorArea.value = await readOpfsFile(it.path)
    opfsCurrentPath.textContent = it.path
  }
}

document.querySelector("[data-tab=opfs]").addEventListener("click", async () => {
  if (opfsRoot) return
  try {
    opfsRoot = await getMigrationsDir()
    await renderOpfsTree()
  } catch (err) {
    opfsOutput.textContent = "OPFS init failed: " + err.message
    opfsOutput.dataset.status = "err"
  }
})

document.getElementById("opfs-refresh").addEventListener("click", renderOpfsTree)

document.getElementById("opfs-save").addEventListener("click", async () => {
  if (!opfsSelectedPath) return
  await writeOpfsFile(opfsSelectedPath, opfsEditorArea.value)
})

document.getElementById("opfs-new-file").addEventListener("click", async () => {
  const name = prompt("File path (relative to migrations/):")
  if (!name) return
  await writeOpfsFile(name, "")
  await renderOpfsTree()
})

document.getElementById("opfs-new-dir").addEventListener("click", async () => {
  const name = prompt("Directory path (relative to migrations/):")
  if (!name) return
  const parts = name.split("/")
  let dir = opfsRoot
  for (const p of parts) dir = await dir.getDirectoryHandle(p, { create: true })
  await renderOpfsTree()
})

document.getElementById("opfs-delete").addEventListener("click", async () => {
  if (!opfsSelectedPath) return
  if (!confirm(`Delete ${opfsSelectedPath}?`)) return
  await deleteOpfsEntry(opfsSelectedPath)
  opfsSelectedPath = null
  opfsEditorArea.value = ""
  opfsCurrentPath.textContent = "(no file selected)"
  await renderOpfsTree()
})

document.getElementById("opfs-reset").addEventListener("click", async () => {
  if (!confirm("Wipe OPFS migrations dir and re-seed demo files?")) return
  opfsRoot = await getMigrationsDir({ resetSeed: true })
  opfsSelectedPath = null
  opfsEditorArea.value = ""
  opfsCurrentPath.textContent = "(no file selected)"
  await renderOpfsTree()
})

document.getElementById("opfs-apply").addEventListener("click", async () => {
  opfsOutput.textContent = "running…"
  opfsOutput.dataset.status = "idle"
  try {
    if (!opfsRoot) opfsRoot = await getMigrationsDir()
    const db = new PGlite()
    await db.waitReady
    await mm.apply(db, { fs: mmfs.fromOPFS(opfsRoot), applyCurrent: true })
    await reportSchema(db, opfsOutput)
  } catch (err) {
    showError(opfsOutput, err)
  }
})
