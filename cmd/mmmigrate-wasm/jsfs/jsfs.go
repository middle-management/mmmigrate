//go:build js && wasm

// Package jsfs adapts a JS-side filesystem object into Go's io/fs.FS so the
// mmmigrate engine can read migrations regardless of where they live in the
// host (object literal, OPFS, Node fs/promises, fetch, etc.).
//
// The JS adapter is a duck-typed object:
//
//	{
//	  readDir(path):  Promise<Array<{ name: string, isDir: boolean }>>,
//	  readFile(path): Promise<Uint8Array>,
//	}
//
// All paths are forward-slash, rooted at "." (the migrations directory). The
// engine never asks for paths with "..", so adapters do not need to defend
// against traversal.
package jsfs

import (
	"errors"
	"io"
	"io/fs"
	"syscall/js"
	"time"
)

// New wraps a JS filesystem adapter as a Go fs.FS.
func New(adapter js.Value) fs.FS {
	return &jsFS{adapter: adapter}
}

type jsFS struct{ adapter js.Value }

func (f *jsFS) Open(name string) (fs.File, error) {
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrInvalid}
	}
	if name == "." {
		entries, err := f.readDir(".")
		if err != nil {
			return nil, &fs.PathError{Op: "open", Path: name, Err: err}
		}
		return &jsDir{name: name, entries: entries}, nil
	}
	data, err := f.readFile(name)
	if err != nil {
		return nil, &fs.PathError{Op: "open", Path: name, Err: err}
	}
	return &jsFile{name: name, data: data}, nil
}

func (f *jsFS) ReadDir(name string) ([]fs.DirEntry, error) {
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "readdir", Path: name, Err: fs.ErrInvalid}
	}
	return f.readDir(name)
}

func (f *jsFS) ReadFile(name string) ([]byte, error) {
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "readfile", Path: name, Err: fs.ErrInvalid}
	}
	return f.readFile(name)
}

var (
	_ fs.FS         = (*jsFS)(nil)
	_ fs.ReadDirFS  = (*jsFS)(nil)
	_ fs.ReadFileFS = (*jsFS)(nil)
)

func (f *jsFS) readDir(name string) ([]fs.DirEntry, error) {
	res, err := await(f.adapter.Call("readDir", name))
	if err != nil {
		return nil, err
	}
	if res.IsUndefined() || res.IsNull() {
		return nil, nil
	}
	n := res.Length()
	out := make([]fs.DirEntry, 0, n)
	for i := 0; i < n; i++ {
		e := res.Index(i)
		out = append(out, &jsDirEntry{
			name:  e.Get("name").String(),
			isDir: e.Get("isDir").Bool(),
		})
	}
	return out, nil
}

func (f *jsFS) readFile(name string) ([]byte, error) {
	res, err := await(f.adapter.Call("readFile", name))
	if err != nil {
		return nil, err
	}
	if res.IsUndefined() || res.IsNull() {
		return nil, fs.ErrNotExist
	}
	n := res.Length()
	buf := make([]byte, n)
	js.CopyBytesToGo(buf, res)
	return buf, nil
}

// --- fs.File / DirEntry shims ---

type jsFile struct {
	name string
	data []byte
	pos  int
}

func (f *jsFile) Stat() (fs.FileInfo, error) {
	return &jsInfo{name: f.name, size: int64(len(f.data))}, nil
}

func (f *jsFile) Read(p []byte) (int, error) {
	if f.pos >= len(f.data) {
		return 0, io.EOF
	}
	n := copy(p, f.data[f.pos:])
	f.pos += n
	return n, nil
}

func (f *jsFile) Close() error { return nil }

type jsDir struct {
	name    string
	entries []fs.DirEntry
	pos     int
}

func (d *jsDir) Stat() (fs.FileInfo, error) {
	return &jsInfo{name: d.name, isDir: true}, nil
}

func (d *jsDir) Read(_ []byte) (int, error) {
	return 0, errors.New("jsfs: cannot read directory as file")
}

func (d *jsDir) Close() error { return nil }

func (d *jsDir) ReadDir(n int) ([]fs.DirEntry, error) {
	remaining := len(d.entries) - d.pos
	if remaining == 0 {
		if n <= 0 {
			return nil, nil
		}
		return nil, io.EOF
	}
	take := remaining
	if n > 0 && n < take {
		take = n
	}
	out := d.entries[d.pos : d.pos+take]
	d.pos += take
	return out, nil
}

type jsDirEntry struct {
	name  string
	isDir bool
}

func (e *jsDirEntry) Name() string               { return e.name }
func (e *jsDirEntry) IsDir() bool                { return e.isDir }
func (e *jsDirEntry) Type() fs.FileMode          { return e.mode() }
func (e *jsDirEntry) Info() (fs.FileInfo, error) { return &jsInfo{name: e.name, isDir: e.isDir}, nil }
func (e *jsDirEntry) mode() fs.FileMode {
	if e.isDir {
		return fs.ModeDir
	}
	return 0
}

type jsInfo struct {
	name  string
	size  int64
	isDir bool
}

func (i *jsInfo) Name() string { return i.name }
func (i *jsInfo) Size() int64  { return i.size }
func (i *jsInfo) Mode() fs.FileMode {
	if i.isDir {
		return fs.ModeDir | 0555
	}
	return 0444
}
func (i *jsInfo) ModTime() time.Time { return time.Time{} }
func (i *jsInfo) IsDir() bool        { return i.isDir }
func (i *jsInfo) Sys() any           { return nil }
