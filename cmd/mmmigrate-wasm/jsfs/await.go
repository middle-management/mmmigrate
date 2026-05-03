//go:build js && wasm

package jsfs

import (
	"errors"
	"syscall/js"
)

// await blocks the current goroutine until the JS Promise (or plain value)
// settles. If the JS adapter returns a non-Promise value synchronously, we
// pass it through.
func await(p js.Value) (js.Value, error) {
	if !isPromise(p) {
		return p, nil
	}

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

func isPromise(v js.Value) bool {
	if v.IsUndefined() || v.IsNull() {
		return false
	}
	if v.Type() != js.TypeObject {
		return false
	}
	then := v.Get("then")
	return !then.IsUndefined() && then.Type() == js.TypeFunction
}

func jsError(args []js.Value) error {
	if len(args) == 0 {
		return errors.New("jsfs: unknown error")
	}
	v := args[0]
	if v.IsUndefined() || v.IsNull() {
		return errors.New("jsfs: unknown error")
	}
	if msg := v.Get("message"); !msg.IsUndefined() && !msg.IsNull() {
		return errors.New(msg.String())
	}
	return errors.New(v.String())
}
