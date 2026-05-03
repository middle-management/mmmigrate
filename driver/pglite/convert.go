//go:build js && wasm

package pglite

import (
	"database/sql/driver"
	"fmt"
	"syscall/js"
	"time"
)

// toJS converts a Go driver.Value to a JS value suitable for pglite query
// parameter binding. database/sql normalizes args to: nil, int64, float64,
// bool, []byte, string, time.Time.
func toJS(v driver.Value) js.Value {
	switch x := v.(type) {
	case nil:
		return js.Null()
	case bool:
		return js.ValueOf(x)
	case int64:
		// JS numbers are float64; values within safe integer range round-trip.
		return js.ValueOf(float64(x))
	case float64:
		return js.ValueOf(x)
	case string:
		return js.ValueOf(x)
	case []byte:
		u8 := js.Global().Get("Uint8Array").New(len(x))
		js.CopyBytesToJS(u8, x)
		return u8
	case time.Time:
		return js.ValueOf(x.UTC().Format(time.RFC3339Nano))
	default:
		return js.ValueOf(fmt.Sprint(x))
	}
}

// fromJS converts a JS value (a column value from pglite) to a Go
// driver.Value. database/sql handles further conversion to the destination
// Go type during Rows.Scan.
func fromJS(v js.Value) driver.Value {
	switch v.Type() {
	case js.TypeNull, js.TypeUndefined:
		return nil
	case js.TypeBoolean:
		return v.Bool()
	case js.TypeNumber:
		f := v.Float()
		if f == float64(int64(f)) {
			return int64(f)
		}
		return f
	case js.TypeString:
		return v.String()
	case js.TypeObject:
		// pglite returns Date objects for timestamp columns and Uint8Array
		// for bytea. Anything else (e.g. JSON objects) is best-effort
		// stringified via JSON.stringify.
		if date := js.Global().Get("Date"); !date.IsUndefined() && v.InstanceOf(date) {
			return v.Call("toISOString").String()
		}
		if u8 := js.Global().Get("Uint8Array"); !u8.IsUndefined() && v.InstanceOf(u8) {
			n := v.Length()
			b := make([]byte, n)
			js.CopyBytesToGo(b, v)
			return b
		}
		if json := js.Global().Get("JSON"); !json.IsUndefined() {
			return json.Call("stringify", v).String()
		}
		return nil
	default:
		return nil
	}
}

// argsToJSArray turns named driver args into a JS Array for pglite's
// .query(sql, params) call.
func argsToJSArray(args []driver.NamedValue) js.Value {
	arr := js.Global().Get("Array").New(len(args))
	for i, a := range args {
		arr.SetIndex(i, toJS(a.Value))
	}
	return arr
}
