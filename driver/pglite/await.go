//go:build js && wasm

package pglite

import (
	"errors"
	"syscall/js"
)

// await blocks the current goroutine until the JS Promise settles, then
// returns its resolved value or rejection reason. The Go scheduler yields
// to the JS event loop while waiting on the channel, which is what allows
// the Promise to actually run.
func await(p js.Value) (js.Value, error) {
	type result struct {
		val js.Value
		err error
	}
	ch := make(chan result, 1)

	var thenFn, catchFn js.Func
	thenFn = js.FuncOf(func(this js.Value, args []js.Value) any {
		var v js.Value
		if len(args) > 0 {
			v = args[0]
		}
		ch <- result{val: v}
		return nil
	})
	catchFn = js.FuncOf(func(this js.Value, args []js.Value) any {
		ch <- result{err: jsError(args)}
		return nil
	})

	p.Call("then", thenFn, catchFn)
	r := <-ch
	thenFn.Release()
	catchFn.Release()
	return r.val, r.err
}

func jsError(args []js.Value) error {
	if len(args) == 0 {
		return errors.New("pglite: unknown error")
	}
	v := args[0]
	if v.IsUndefined() || v.IsNull() {
		return errors.New("pglite: unknown error")
	}
	if msg := v.Get("message"); !msg.IsUndefined() && !msg.IsNull() {
		return errors.New(msg.String())
	}
	return errors.New(v.String())
}
