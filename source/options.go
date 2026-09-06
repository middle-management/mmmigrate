package source

import (
	"fmt"
	"io/fs"
	"path"
	"strings"
)

// Option configures where mmmigrate looks for migration files.
type Option func(*config)

// config is the resolved form of the Options passed to a call.
type config struct {
	// committed is the slash-separated subdirectory of the migrations
	// directory that holds numbered migrations. Empty means they sit at the
	// root, alongside current.sql.
	committed string
}

// WithCommittedDir places numbered migrations in a subdirectory of the
// migrations directory instead of alongside current.sql. current.sql and
// @include paths are always resolved from the migrations root regardless.
//
// This exists mainly for projects coming from Graphile Migrate, whose layout
// is migrations/current.sql plus migrations/committed/NNNNNN-name.sql:
// WithCommittedDir("committed") reads that tree in place. An empty string or
// "." selects the default flat layout.
func WithCommittedDir(dir string) Option {
	return func(c *config) {
		c.committed = strings.Trim(strings.TrimSpace(dir), "/")
	}
}

// newConfig applies opts and validates the result.
func newConfig(opts []Option) (config, error) {
	var c config
	for _, opt := range opts {
		opt(&c)
	}

	if c.committed == "" {
		return c, nil
	}

	c.committed = path.Clean(c.committed)
	if c.committed == "." {
		c.committed = ""
		return c, nil
	}

	// fs.ValidPath rejects absolute paths and anything containing "..", which
	// is exactly what must not escape the migrations directory.
	if !fs.ValidPath(c.committed) {
		return c, fmt.Errorf("invalid committed directory %q: must be a relative path inside the migrations directory", c.committed)
	}

	return c, nil
}

// dir returns the slash-separated directory holding numbered migrations,
// relative to the migrations root. "." when the layout is flat.
func (c config) dir() string {
	if c.committed == "" {
		return "."
	}
	return c.committed
}

// join returns the path of a committed migration file relative to the
// migrations root.
func (c config) join(filename string) string {
	if c.committed == "" {
		return filename
	}
	return path.Join(c.committed, filename)
}
