//go:build js && wasm

package jsdb

import (
	"database/sql/driver"
	"io"
	"syscall/js"
)

// rows wraps a JS-side query result: { fields: [{name,...}], rows: [{col: val, ...}] }.
// We snapshot field names and row values up front because results are
// returned synchronously from a single Promise resolution.
type rows struct {
	cols []string
	data []js.Value
	pos  int
}

func newRows(res js.Value) *rows {
	r := &rows{}
	if fields := res.Get("fields"); !fields.IsUndefined() && !fields.IsNull() {
		n := fields.Length()
		r.cols = make([]string, n)
		for i := 0; i < n; i++ {
			r.cols[i] = fields.Index(i).Get("name").String()
		}
	}
	if data := res.Get("rows"); !data.IsUndefined() && !data.IsNull() {
		n := data.Length()
		r.data = make([]js.Value, n)
		for i := 0; i < n; i++ {
			r.data[i] = data.Index(i)
		}
	}
	return r
}

func (r *rows) Columns() []string { return r.cols }
func (r *rows) Close() error      { return nil }

func (r *rows) Next(dest []driver.Value) error {
	if r.pos >= len(r.data) {
		return io.EOF
	}
	row := r.data[r.pos]
	r.pos++
	for i, col := range r.cols {
		dest[i] = fromJS(row.Get(col))
	}
	return nil
}

var _ driver.Rows = (*rows)(nil)
