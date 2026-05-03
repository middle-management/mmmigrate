// JS glue for mmmigrate-wasm.
//
// Usage:
//   import { loadMmmigrate, fs as mmfs } from './mmmigrate.mjs'
//   const mm = await loadMmmigrate(fetch('./mmmigrate.wasm'))
//   await mm.apply(pgliteDb, { fs: mmfs.fromMap({ '001_users.sql': '...' }) })
//
// The wasm runtime requires Go's `wasm_exec.js` shim to be loaded before
// instantiation. In the browser, include it via a <script> tag (it sets
// globalThis.Go) or import the equivalent ESM polyfill. In Node 18+, a copy
// of `wasm_exec.js` is bundled alongside this module — see test/run.mjs.

/**
 * Instantiate the wasm and return the namespace it exposes.
 *
 * @param {Response | ArrayBuffer | Uint8Array | Promise<Response | ArrayBuffer | Uint8Array>} source
 * @param {object} [opts]
 * @param {object} [opts.Go] Constructor for Go's wasm runtime. Defaults to globalThis.Go.
 * @returns {Promise<{ apply: Function }>} the global mmmigrate namespace
 */
export async function loadMmmigrate(source, opts = {}) {
  const GoCtor = opts.Go ?? globalThis.Go
  if (!GoCtor) {
    throw new Error(
      "loadMmmigrate: globalThis.Go is not defined. Load wasm_exec.js first.",
    )
  }
  const go = new GoCtor()

  const resolved = await source
  let bytes
  if (resolved instanceof Response) {
    bytes = await resolved.arrayBuffer()
  } else if (resolved instanceof Uint8Array) {
    bytes = resolved
  } else {
    bytes = resolved
  }

  const { instance } = await WebAssembly.instantiate(bytes, go.importObject)
  // run() resolves only when main() returns. Our main blocks on select{} so
  // we don't await it; the runtime is left running for our js.Func handlers.
  go.run(instance)
  if (!globalThis.mmmigrate) {
    throw new Error("mmmigrate: wasm did not register a global namespace")
  }
  return globalThis.mmmigrate
}

// --- FS adapters ---
//
// Each adapter is a duck-typed object:
//   { readDir(path):  Promise<Array<{name, isDir}>>,
//     readFile(path): Promise<Uint8Array> }
//
// All paths are forward-slash and rooted at "."; the wasm side never asks
// for ".." or absolute paths.

export const fs = {
  fromMap,
  fromOPFS,
  fromNodeFS,
}

/**
 * In-memory FS backed by an object literal mapping forward-slash paths to
 * either a string or a Uint8Array.
 *
 *   fromMap({
 *     '001_users.sql': 'CREATE TABLE users(id int);',
 *     'functions/helper.sql': '...',
 *     'current.sql': '',
 *   })
 */
export function fromMap(files) {
  const enc = new TextEncoder()
  // Normalize keys (drop leading "./", coerce backslashes).
  const norm = {}
  for (const [k, v] of Object.entries(files)) {
    const key = k.replace(/\\/g, "/").replace(/^\.\//, "")
    norm[key] = typeof v === "string" ? enc.encode(v) : v
  }
  return {
    async readDir(path) {
      const prefix = path === "." || path === "" ? "" : path.replace(/\/$/, "") + "/"
      const seen = new Set()
      const out = []
      for (const key of Object.keys(norm)) {
        if (!key.startsWith(prefix)) continue
        const rest = key.slice(prefix.length)
        if (!rest) continue
        const slash = rest.indexOf("/")
        if (slash < 0) {
          if (!seen.has(rest)) {
            seen.add(rest)
            out.push({ name: rest, isDir: false })
          }
        } else {
          const sub = rest.slice(0, slash)
          if (!seen.has(sub)) {
            seen.add(sub)
            out.push({ name: sub, isDir: true })
          }
        }
      }
      return out
    },
    async readFile(path) {
      const key = path.replace(/^\.\//, "")
      const data = norm[key]
      if (!data) {
        throw new Error(`mmmigrate.fs.fromMap: file not found: ${path}`)
      }
      return data
    },
  }
}

/**
 * Browser FS adapter backed by an OPFS FileSystemDirectoryHandle, e.g.
 *
 *   const root = await navigator.storage.getDirectory()
 *   const dir  = await root.getDirectoryHandle('migrations', { create: true })
 *   const fsAdapter = mmmigrate.fs.fromOPFS(dir)
 */
export function fromOPFS(rootHandle) {
  async function navigate(path, { wantFile }) {
    if (path === "." || path === "") {
      if (wantFile) {
        throw new Error("mmmigrate.fs.fromOPFS: '.' is a directory, not a file")
      }
      return rootHandle
    }
    const parts = path.split("/").filter(Boolean)
    const last = parts.pop()
    let dir = rootHandle
    for (const part of parts) {
      dir = await dir.getDirectoryHandle(part, { create: false })
    }
    if (wantFile) {
      const fh = await dir.getFileHandle(last, { create: false })
      return await fh.getFile()
    }
    return await dir.getDirectoryHandle(last, { create: false })
  }
  return {
    async readDir(path) {
      const dir = await navigate(path, { wantFile: false })
      const out = []
      for await (const [name, handle] of dir.entries()) {
        out.push({ name, isDir: handle.kind === "directory" })
      }
      return out
    },
    async readFile(path) {
      const file = await navigate(path, { wantFile: true })
      return new Uint8Array(await file.arrayBuffer())
    },
  }
}

/**
 * Node FS adapter backed by `fs/promises`. Pass an absolute or
 * cwd-relative directory path. Useful for the Node smoke test and any
 * server-side use.
 *
 *   import { fs as mmfs } from './mmmigrate.mjs'
 *   const fsAdapter = await mmfs.fromNodeFS('./migrations')
 */
export async function fromNodeFS(dirPath) {
  const fsp = await import("node:fs/promises")
  const path = await import("node:path")
  return {
    async readDir(p) {
      const target = p === "." ? dirPath : path.join(dirPath, p)
      const entries = await fsp.readdir(target, { withFileTypes: true })
      return entries.map((e) => ({ name: e.name, isDir: e.isDirectory() }))
    },
    async readFile(p) {
      const buf = await fsp.readFile(path.join(dirPath, p))
      return new Uint8Array(buf.buffer, buf.byteOffset, buf.byteLength)
    },
  }
}
