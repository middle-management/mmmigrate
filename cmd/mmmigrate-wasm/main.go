//go:build js && wasm

// Command mmmigrate-wasm exposes a JS-callable interface to the mmmigrate
// engine for use against pglite (a WASM build of PostgreSQL).
//
// The wasm exposes one function on the global object:
//
//	mmmigrate.apply(pgliteDb, options) -> Promise<{ applied: string[] }>
//
// where pgliteDb is a PGlite instance and options is:
//
//	{
//	  fs:           { readDir(p): Promise<entries>, readFile(p): Promise<bytes> },
//	  applyCurrent: bool,  // default true
//	}
//
// A companion JS module (glue/mmmigrate.mjs) ships ready-made fs adapters
// for inline objects, OPFS, and Node fs/promises.
package main

import (
	"context"
	"database/sql"
	"fmt"
	"syscall/js"

	"github.com/middle-management/mmmigrate"
	"github.com/middle-management/mmmigrate/cmd/mmmigrate-wasm/jsfs"
	"github.com/middle-management/mmmigrate/driver/pglite"
)

func main() {
	api := js.Global().Get("Object").New()
	api.Set("apply", js.FuncOf(apply))
	js.Global().Set("mmmigrate", api)

	// Block forever so the wasm runtime stays alive for the host's calls.
	select {}
}

// apply: (pgliteDb: PGlite, options: { fs, applyCurrent? }) -> Promise
func apply(this js.Value, args []js.Value) any {
	if len(args) < 2 {
		return rejectedPromise(fmt.Errorf("mmmigrate.apply: expected (pgliteDb, options)"))
	}
	db := args[0]
	opts := args[1]

	fsAdapter := opts.Get("fs")
	if fsAdapter.IsUndefined() || fsAdapter.IsNull() {
		return rejectedPromise(fmt.Errorf("mmmigrate.apply: options.fs is required"))
	}
	applyCurrent := true
	if v := opts.Get("applyCurrent"); !v.IsUndefined() && !v.IsNull() {
		applyCurrent = v.Bool()
	}

	return goPromise(func() (any, error) {
		conn := sql.OpenDB(pglite.NewConnector(db))
		defer conn.Close()

		fsys := jsfs.New(fsAdapter)

		ctx := context.Background()
		if err := mmmigrate.RunMigrations(ctx, conn, pglite.Dialect{}, fsys, applyCurrent); err != nil {
			return nil, err
		}
		return js.ValueOf(map[string]any{"ok": true}), nil
	})
}

// goPromise constructs a JS Promise resolved/rejected from a Go goroutine.
// The work function must not be called on the main goroutine because every
// pglite call awaits a JS Promise, which would deadlock if we blocked the
// JS event loop.
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
	// The executor is invoked synchronously by the Promise constructor; it is
	// safe to release immediately afterward because the promise machinery
	// won't reuse it.
	defer executor.Release()
	return js.Global().Get("Promise").New(executor)
}

func rejectedPromise(err error) js.Value {
	reject := js.Global().Get("Promise").Get("reject")
	return reject.Invoke(js.Global().Get("Error").New(err.Error()))
}
