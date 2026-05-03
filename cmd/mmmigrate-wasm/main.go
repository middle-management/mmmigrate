//go:build js && wasm

// Command mmmigrate-wasm exposes a JS-callable interface to the mmmigrate
// engine for use against pglite (a WASM build of PostgreSQL) or
// sqlite-wasm (the official SQLite WASM build).
//
// The wasm exposes two functions on the global object:
//
//	mmmigrate.apply(jsDb, options)   -> Promise<{ ok: true }>
//	mmmigrate.render(options)        -> Promise<string>
//
// where jsDb is a JS object exposing the pglite-shaped contract
// ({.exec(sql), .query(sql, params)}) and options is:
//
//	{
//	  fs:           { readDir(p): Promise<entries>, readFile(p): Promise<bytes> },
//	  dialect:      'postgres' | 'sqlite',  // default 'postgres'
//	  applyCurrent: bool,                   // default true (apply only)
//	}
//
// A companion JS module (glue/mmmigrate.mjs) ships ready-made adapters for
// pglite, @sqlite.org/sqlite-wasm, and three FS sources.
package main

import (
	"context"
	"database/sql"
	"fmt"
	"syscall/js"

	"github.com/middle-management/mmmigrate"
	"github.com/middle-management/mmmigrate/cmd/mmmigrate-wasm/jsfs"
	"github.com/middle-management/mmmigrate/driver/jsdb"
	"github.com/middle-management/mmmigrate/driver/pglite"
	"github.com/middle-management/mmmigrate/driver/sqlitejs"
	"github.com/middle-management/mmmigrate/source"
)

func main() {
	api := js.Global().Get("Object").New()
	api.Set("apply", js.FuncOf(apply))
	api.Set("render", js.FuncOf(render))
	js.Global().Set("mmmigrate", api)

	// Block forever so the wasm runtime stays alive for the host's calls.
	select {}
}

// apply: (jsDb, { fs, dialect?, applyCurrent? }) -> Promise<{ ok: true }>
func apply(this js.Value, args []js.Value) any {
	if len(args) < 2 {
		return rejectedPromise(fmt.Errorf("mmmigrate.apply: expected (jsDb, options)"))
	}
	db := args[0]
	opts := args[1]

	fsAdapter := opts.Get("fs")
	if fsAdapter.IsUndefined() || fsAdapter.IsNull() {
		return rejectedPromise(fmt.Errorf("mmmigrate.apply: options.fs is required"))
	}

	dialect, err := dialectFromOpts(opts)
	if err != nil {
		return rejectedPromise(err)
	}

	applyCurrent := true
	if v := opts.Get("applyCurrent"); !v.IsUndefined() && !v.IsNull() {
		applyCurrent = v.Bool()
	}

	return goPromise(func() (any, error) {
		conn := sql.OpenDB(jsdb.NewConnector(db))
		defer conn.Close()

		fsys := jsfs.New(fsAdapter)
		ctx := context.Background()
		if err := mmmigrate.RunMigrations(ctx, conn, dialect, fsys, applyCurrent); err != nil {
			return nil, err
		}
		return js.ValueOf(map[string]any{"ok": true}), nil
	})
}

// render: ({ fs }) -> Promise<string>
//
// Returns the expanded current.sql (with @include directives inlined) so a
// caller can preview what would be executed without running it.
func render(this js.Value, args []js.Value) any {
	if len(args) < 1 {
		return rejectedPromise(fmt.Errorf("mmmigrate.render: expected (options)"))
	}
	opts := args[0]

	fsAdapter := opts.Get("fs")
	if fsAdapter.IsUndefined() || fsAdapter.IsNull() {
		return rejectedPromise(fmt.Errorf("mmmigrate.render: options.fs is required"))
	}

	return goPromise(func() (any, error) {
		fsys := jsfs.New(fsAdapter)
		out, err := source.Render(fsys)
		if err != nil {
			return nil, err
		}
		return js.ValueOf(out), nil
	})
}

func dialectFromOpts(opts js.Value) (mmmigrate.Dialect, error) {
	v := opts.Get("dialect")
	if v.IsUndefined() || v.IsNull() {
		return pglite.Dialect{}, nil
	}
	switch v.String() {
	case "postgres", "pglite":
		return pglite.Dialect{}, nil
	case "sqlite", "sqlite-wasm":
		return sqlitejs.Dialect{}, nil
	default:
		return nil, fmt.Errorf("mmmigrate: unknown dialect %q (want 'postgres' or 'sqlite')", v.String())
	}
}

// goPromise constructs a JS Promise resolved/rejected from a Go goroutine.
// The work function must not be called on the main goroutine because every
// db call awaits a JS Promise, which would deadlock if we blocked the JS
// event loop.
func goPromise(work func() (any, error)) js.Value {
	executor := js.FuncOf(func(this js.Value, args []js.Value) any {
		resolve := args[0]
		reject := args[1]
		go func() {
			result, err := work()
			if err != nil {
				reject.Invoke(js.Global().Get("Error").New(err.Error()))
				return
			}
			resolve.Invoke(result)
		}()
		return nil
	})
	defer executor.Release()
	return js.Global().Get("Promise").New(executor)
}

func rejectedPromise(err error) js.Value {
	reject := js.Global().Get("Promise").Get("reject")
	return reject.Invoke(js.Global().Get("Error").New(err.Error()))
}
