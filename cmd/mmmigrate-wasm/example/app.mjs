// mmmigrate playground.
//
// Two tabs (Inline and OPFS), one CodeMirror editor each, a dialect dropdown
// in the header, and Apply / Render buttons that talk to the wasm.

import { EditorView, basicSetup } from "https://esm.sh/codemirror@6.0.1"
import { sql, PostgreSQL, SQLite } from "https://esm.sh/@codemirror/lang-sql@6.8.0"
import { EditorState, Compartment } from "https://esm.sh/@codemirror/state@6"
import { PGlite } from "https://cdn.jsdelivr.net/npm/@electric-sql/pglite@0.2.17/dist/index.js"
// sqlite-wasm needs its sidecar .wasm at a relative URL, so use jsdelivr
// (preserves package structure) rather than esm.sh.
import sqlite3InitModule from "https://cdn.jsdelivr.net/npm/@sqlite.org/sqlite-wasm@3.46.1-build5/index.mjs"
import { loadMmmigrate, fs as mmfs, db as mmdb } from "../glue/mmmigrate.mjs"

// ── boot wasm ─────────────────────────────────────────────────────────

const mm = await loadMmmigrate(fetch("../mmmigrate.wasm"))

// ── seeds ─────────────────────────────────────────────────────────────

const SEEDS = {
  postgres: {
    "001_users.sql":
      "CREATE TABLE users (\n  id   INT PRIMARY KEY,\n  name TEXT NOT NULL\n);\n",
    "functions/touch_updated_at.sql":
      "CREATE OR REPLACE FUNCTION touch_updated_at() RETURNS trigger\n" +
      "LANGUAGE plpgsql AS $$\nBEGIN\n  NEW.updated_at = now();\n  RETURN NEW;\nEND $$;\n",
    "current.sql":
      "-- current.sql is re-applied on every run while developing.\n" +
      "ALTER TABLE users ADD COLUMN IF NOT EXISTS bio TEXT;\n" +
      "ALTER TABLE users ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ DEFAULT now();\n" +
      "\n-- @include functions/touch_updated_at.sql\n\n" +
      "DROP TRIGGER IF EXISTS users_touch ON users;\n" +
      "CREATE TRIGGER users_touch BEFORE UPDATE ON users\n" +
      "  FOR EACH ROW EXECUTE FUNCTION touch_updated_at();\n",
  },
  sqlite: {
    "001_users.sql":
      "CREATE TABLE users (\n  id   INTEGER PRIMARY KEY,\n  name TEXT NOT NULL\n);\n",
    "views/active_users.sql":
      "CREATE VIEW IF NOT EXISTS active_users AS\n" +
      "  SELECT id, name FROM users WHERE active = 1;\n",
    "current.sql":
      "-- current.sql is re-applied on every run while developing.\n" +
      "-- (SQLite has no IF NOT EXISTS for ADD COLUMN; the playground gives\n" +
      "-- you a fresh DB on every Apply so this is fine.)\n" +
      "ALTER TABLE users ADD COLUMN active INTEGER DEFAULT 1;\n" +
      "\n-- @include views/active_users.sql\n",
  },
}

const STARTING_FILE = "current.sql"
const INCLUDE_RE = /^\s*--\s*@include\s+(.+?)\s*$/gm

// ── state ─────────────────────────────────────────────────────────────

let dialect = "postgres"
let sqlite3 = null            // lazily initialized

const inline = {
  files: { ...SEEDS.postgres },
  active: STARTING_FILE,
  view: null,
  langCompartment: new Compartment(),
}

const opfs = {
  root: null,                 // FileSystemDirectoryHandle for migrations dir
  active: null,
  view: null,
  initialized: false,
  langCompartment: new Compartment(),
}

// ── helpers ───────────────────────────────────────────────────────────

function findIncludes(source) {
  return [...(source ?? "").matchAll(INCLUDE_RE)].map((m) => m[1].trim())
}

function langExtensionFor(dialect) {
  return sql({ dialect: dialect === "sqlite" ? SQLite : PostgreSQL })
}

function makeEditor({ parent, doc, langCompartment, onChange }) {
  return new EditorView({
    state: EditorState.create({
      doc,
      extensions: [
        basicSetup,
        langCompartment.of(langExtensionFor(dialect)),
        EditorView.updateListener.of((u) => {
          if (u.docChanged) onChange(u.state.doc.toString())
        }),
      ],
    }),
    parent,
  })
}

function setEditorDoc(view, doc) {
  view.dispatch({
    changes: { from: 0, to: view.state.doc.length, insert: doc },
  })
}

async function freshDB() {
  if (dialect === "postgres") {
    const db = new PGlite()
    await db.waitReady
    return { handle: db, raw: db }
  } else {
    if (!sqlite3) sqlite3 = await sqlite3InitModule()
    const raw = new sqlite3.oo1.DB(":memory:")
    return { handle: mmdb.adaptSQLiteWasm(raw), raw }
  }
}

async function reportSchema(out, raw) {
  let applied, tables
  if (dialect === "postgres") {
    applied = (
      await raw.query(
        "SELECT version, name, applied_at FROM mmmigrate.applied ORDER BY version",
      )
    ).rows
    tables = (
      await raw.query(
        `SELECT table_name FROM information_schema.tables
           WHERE table_schema = 'public' ORDER BY table_name`,
      )
    ).rows.map((r) => r.table_name)
  } else {
    applied = []
    raw.exec({
      sql: "SELECT version, name, applied_at FROM mmmigrate_applied ORDER BY version",
      rowMode: "object",
      callback: (r) => applied.push(r),
    })
    tables = []
    raw.exec({
      sql: "SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' AND name <> 'mmmigrate_applied' AND name <> 'mmmigrate_current' ORDER BY name",
      rowMode: "object",
      callback: (r) => tables.push(r.name),
    })
  }
  out.textContent =
    `✓ apply succeeded (${dialect})\n\napplied:\n` +
    applied
      .map(
        (r) =>
          `  ${String(r.version).padStart(3, "0")}  ${r.name}  (${r.applied_at})`,
      )
      .join("\n") +
    "\n\ntables:\n" +
    (tables.length ? tables.map((t) => "  " + t).join("\n") : "  (none)")
  out.dataset.status = "ok"
}

function showError(out, err) {
  out.textContent = "✗ " + (err?.stack || err?.message || String(err))
  out.dataset.status = "err"
}

function showInfo(out, text) {
  out.textContent = text
  out.dataset.status = "info"
}

// ── INLINE TAB ────────────────────────────────────────────────────────

const inlineFilesEl = document.getElementById("inline-files")
const inlineEditorEl = document.getElementById("inline-editor")
const inlineCurrentPath = document.getElementById("inline-current-path")
const inlineIncludesEl = document.getElementById("inline-includes")
const inlineOutput = document.getElementById("inline-output")

inline.view = makeEditor({
  parent: inlineEditorEl,
  doc: inline.files[inline.active] ?? "",
  langCompartment: inline.langCompartment,
  onChange: (doc) => {
    inline.files[inline.active] = doc
    if (inline.active === "current.sql") renderInlineIncludes()
  },
})

function renderInlineFiles() {
  inlineFilesEl.innerHTML = ""
  for (const name of Object.keys(inline.files).sort()) {
    const li = document.createElement("li")
    li.textContent = name
    li.className = "file"
    if (name === inline.active) li.classList.add("selected")
    li.addEventListener("click", () => selectInlineFile(name))
    inlineFilesEl.append(li)
  }
}

function selectInlineFile(name) {
  if (!(name in inline.files)) return
  inline.active = name
  inlineCurrentPath.textContent = name
  setEditorDoc(inline.view, inline.files[name])
  renderInlineFiles()
  renderInlineIncludes()
}

function renderInlineIncludes() {
  inlineIncludesEl.innerHTML = ""
  if (inline.active !== "current.sql") return
  const incs = findIncludes(inline.files[inline.active])
  if (!incs.length) return
  const label = document.createElement("span")
  label.textContent = "@includes:"
  label.className = "label"
  inlineIncludesEl.append(label)
  for (const path of incs) {
    const b = document.createElement("span")
    b.className = "badge"
    b.textContent = path
    if (path in inline.files) {
      b.title = "Open " + path
      b.addEventListener("click", () => selectInlineFile(path))
    } else {
      b.title = path + " (missing)"
      b.style.opacity = "0.5"
    }
    inlineIncludesEl.append(b)
  }
}

document.getElementById("inline-apply").addEventListener("click", async () => {
  showInfo(inlineOutput, "running…")
  try {
    const { handle, raw } = await freshDB()
    await mm.apply(handle, {
      fs: mmfs.fromMap(inline.files),
      dialect,
      applyCurrent: true,
    })
    await reportSchema(inlineOutput, raw)
  } catch (err) {
    showError(inlineOutput, err)
  }
})

document.getElementById("inline-render").addEventListener("click", async () => {
  showInfo(inlineOutput, "rendering…")
  try {
    const out = await mm.render({ fs: mmfs.fromMap(inline.files) })
    inlineOutput.textContent =
      "// rendered current.sql with includes inlined\n\n" + out
    inlineOutput.dataset.status = "ok"
  } catch (err) {
    showError(inlineOutput, err)
  }
})

// ── OPFS TAB ──────────────────────────────────────────────────────────

const opfsFilesEl = document.getElementById("opfs-tree-list")
const opfsEditorEl = document.getElementById("opfs-editor")
const opfsCurrentPath = document.getElementById("opfs-current-path")
const opfsIncludesEl = document.getElementById("opfs-includes")
const opfsOutput = document.getElementById("opfs-output")
const opfsSaveBtn = document.getElementById("opfs-save")

opfs.view = makeEditor({
  parent: opfsEditorEl,
  doc: "",
  langCompartment: opfs.langCompartment,
  onChange: () => {
    if (opfs.active === "current.sql") renderOpfsIncludes()
    opfsSaveBtn.disabled = false
  },
})
opfsSaveBtn.disabled = true

async function getMigrationsDir({ resetSeed = false } = {}) {
  if (!navigator.storage?.getDirectory) {
    throw new Error("OPFS is not available in this browser")
  }
  const top = await navigator.storage.getDirectory()
  const ns = await top.getDirectoryHandle("mmmigrate-example", { create: true })
  const dialectDir = await ns.getDirectoryHandle(dialect, { create: true })
  const dir = await dialectDir.getDirectoryHandle("migrations", { create: true })

  if (resetSeed) {
    for await (const name of dir.keys()) {
      await dir.removeEntry(name, { recursive: true })
    }
  }

  let empty = true
  for await (const _ of dir.keys()) {
    empty = false
    break
  }
  if (empty) {
    for (const [path, body] of Object.entries(SEEDS[dialect])) {
      await opfsWriteFile(dir, path, body)
    }
  }
  return dir
}

async function opfsWriteFile(dir, path, body) {
  const parts = path.split("/")
  const name = parts.pop()
  let cur = dir
  for (const p of parts) cur = await cur.getDirectoryHandle(p, { create: true })
  const fh = await cur.getFileHandle(name, { create: true })
  const w = await fh.createWritable()
  await w.write(body)
  await w.close()
}

async function listOpfsTree() {
  const items = []
  async function walk(d, prefix) {
    const entries = []
    for await (const [name, handle] of d.entries()) {
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
  await walk(opfs.root, "")
  return items
}

async function renderOpfsTree() {
  const items = await listOpfsTree()
  opfsFilesEl.innerHTML = ""
  for (const it of items) {
    const li = document.createElement("li")
    li.textContent = "  ".repeat(it.depth) + it.name
    li.className = it.isDir ? "dir" : "file"
    li.dataset.path = it.path
    if (it.path === opfs.active) li.classList.add("selected")
    li.addEventListener("click", () => selectOpfsEntry(it))
    opfsFilesEl.append(li)
  }
}

async function readOpfsFile(path) {
  const parts = path.split("/")
  const name = parts.pop()
  let dir = opfs.root
  for (const p of parts) dir = await dir.getDirectoryHandle(p)
  const fh = await dir.getFileHandle(name)
  return await (await fh.getFile()).text()
}

async function deleteOpfsEntry(path) {
  const parts = path.split("/")
  const name = parts.pop()
  let dir = opfs.root
  for (const p of parts) dir = await dir.getDirectoryHandle(p)
  await dir.removeEntry(name, { recursive: true })
}

async function selectOpfsEntry(it) {
  if (it.isDir) return
  opfs.active = it.path
  await renderOpfsTree()
  opfsCurrentPath.textContent = it.path
  setEditorDoc(opfs.view, await readOpfsFile(it.path))
  opfsSaveBtn.disabled = true
  renderOpfsIncludes()
}

function renderOpfsIncludes() {
  opfsIncludesEl.innerHTML = ""
  if (opfs.active !== "current.sql") return
  const doc = opfs.view.state.doc.toString()
  const incs = findIncludes(doc)
  if (!incs.length) return
  const label = document.createElement("span")
  label.textContent = "@includes:"
  label.className = "label"
  opfsIncludesEl.append(label)
  for (const path of incs) {
    const b = document.createElement("span")
    b.className = "badge"
    b.textContent = path
    b.title = "Open " + path
    b.addEventListener("click", async () => {
      try {
        const content = await readOpfsFile(path)
        opfs.active = path
        opfsCurrentPath.textContent = path
        setEditorDoc(opfs.view, content)
        opfsSaveBtn.disabled = true
        await renderOpfsTree()
      } catch {
        alert(`${path} not found in OPFS — create it with "+ file".`)
      }
    })
    opfsIncludesEl.append(b)
  }
}

async function ensureOpfsInit() {
  if (opfs.initialized && opfs.root) return
  opfs.root = await getMigrationsDir()
  opfs.initialized = true
  await renderOpfsTree()
}

document.querySelector("[data-tab=opfs]").addEventListener("click", async () => {
  try {
    await ensureOpfsInit()
  } catch (err) {
    showError(opfsOutput, err)
  }
})

document.getElementById("opfs-refresh").addEventListener("click", async () => {
  await ensureOpfsInit()
  await renderOpfsTree()
})

opfsSaveBtn.addEventListener("click", async () => {
  if (!opfs.active) return
  await opfsWriteFile(opfs.root, opfs.active, opfs.view.state.doc.toString())
  opfsSaveBtn.disabled = true
})

document.getElementById("opfs-new-file").addEventListener("click", async () => {
  await ensureOpfsInit()
  const name = prompt("File path (relative to migrations/):")
  if (!name) return
  await opfsWriteFile(opfs.root, name, "")
  await renderOpfsTree()
})

document.getElementById("opfs-new-dir").addEventListener("click", async () => {
  await ensureOpfsInit()
  const name = prompt("Directory path (relative to migrations/):")
  if (!name) return
  let dir = opfs.root
  for (const p of name.split("/")) dir = await dir.getDirectoryHandle(p, { create: true })
  await renderOpfsTree()
})

document.getElementById("opfs-delete").addEventListener("click", async () => {
  if (!opfs.active) return
  if (!confirm(`Delete ${opfs.active}?`)) return
  await deleteOpfsEntry(opfs.active)
  opfs.active = null
  setEditorDoc(opfs.view, "")
  opfsCurrentPath.textContent = "(no file selected)"
  opfsIncludesEl.innerHTML = ""
  opfsSaveBtn.disabled = true
  await renderOpfsTree()
})

document.getElementById("opfs-reset").addEventListener("click", async () => {
  if (!confirm(`Wipe OPFS migrations for ${dialect} and re-seed?`)) return
  opfs.root = await getMigrationsDir({ resetSeed: true })
  opfs.active = null
  setEditorDoc(opfs.view, "")
  opfsCurrentPath.textContent = "(no file selected)"
  opfsIncludesEl.innerHTML = ""
  opfsSaveBtn.disabled = true
  await renderOpfsTree()
})

document.getElementById("opfs-apply").addEventListener("click", async () => {
  showInfo(opfsOutput, "running…")
  try {
    await ensureOpfsInit()
    const { handle, raw } = await freshDB()
    await mm.apply(handle, {
      fs: mmfs.fromOPFS(opfs.root),
      dialect,
      applyCurrent: true,
    })
    await reportSchema(opfsOutput, raw)
  } catch (err) {
    showError(opfsOutput, err)
  }
})

document.getElementById("opfs-render").addEventListener("click", async () => {
  showInfo(opfsOutput, "rendering…")
  try {
    await ensureOpfsInit()
    const out = await mm.render({ fs: mmfs.fromOPFS(opfs.root) })
    opfsOutput.textContent =
      "// rendered current.sql with includes inlined\n\n" + out
    opfsOutput.dataset.status = "ok"
  } catch (err) {
    showError(opfsOutput, err)
  }
})

// ── Tabs ──────────────────────────────────────────────────────────────

document.querySelectorAll(".tabs button").forEach((btn) => {
  btn.addEventListener("click", () => {
    document.querySelectorAll(".tabs button").forEach((b) => b.classList.remove("active"))
    document.querySelectorAll(".tab").forEach((t) => t.classList.remove("active"))
    btn.classList.add("active")
    document.getElementById("tab-" + btn.dataset.tab).classList.add("active")
  })
})

// ── Dialect switch ────────────────────────────────────────────────────

document.getElementById("dialect").addEventListener("change", async (e) => {
  dialect = e.target.value

  // Reload inline files from this dialect's seed.
  inline.files = { ...SEEDS[dialect] }
  inline.active = STARTING_FILE
  inlineCurrentPath.textContent = STARTING_FILE
  setEditorDoc(inline.view, inline.files[STARTING_FILE])
  renderInlineFiles()
  renderInlineIncludes()
  inline.view.dispatch({
    effects: inline.langCompartment.reconfigure(langExtensionFor(dialect)),
  })
  showInfo(inlineOutput, `engine switched to ${dialect}; seed reloaded`)

  // Reset OPFS state — namespace is per-dialect, and root will be re-resolved
  // on the next tab switch / Apply.
  opfs.initialized = false
  opfs.root = null
  opfs.active = null
  setEditorDoc(opfs.view, "")
  opfsCurrentPath.textContent = "(no file selected)"
  opfsIncludesEl.innerHTML = ""
  opfsFilesEl.innerHTML = ""
  opfsSaveBtn.disabled = true
  opfs.view.dispatch({
    effects: opfs.langCompartment.reconfigure(langExtensionFor(dialect)),
  })
  showInfo(opfsOutput, `engine switched to ${dialect}; switch to OPFS tab to load its files`)
})

// ── Initial render ────────────────────────────────────────────────────

renderInlineFiles()
renderInlineIncludes()
