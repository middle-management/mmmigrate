package source

import (
	"fmt"
	"io/fs"
	"os"
)

// Option configures where mmmigrate looks for migration files.
type Option func(*config)

// config is the resolved form of the Options passed to a call.
type config struct {
	// committedDir is a real filesystem path holding numbered migrations.
	// Empty means they sit alongside current.sql in the migrations directory.
	committedDir string
	// committedFS holds numbered migrations in a virtual filesystem. It takes
	// precedence over committedDir and rules out file-writing operations.
	committedFS fs.FS
}

// WithCommittedDir reads and writes numbered migrations in the given
// directory instead of alongside current.sql. The path is a real filesystem
// path, absolute or relative to the working directory — the same way the CLI's
// -migrations is interpreted — so the committed migrations may live anywhere,
// inside the migrations directory or outside it.
//
// current.sql and @include paths always resolve from the migrations directory
// regardless. An empty string selects the default layout.
//
// A project coming from Graphile Migrate keeps its tree in place with
// WithCommittedDir("migrations/committed").
func WithCommittedDir(dir string) Option {
	return func(c *config) {
		c.committedDir = dir
		c.committedFS = nil
	}
}

// WithCommittedFS reads numbered migrations from an arbitrary filesystem
// rooted at the committed directory — an embedded FS, or an fs.Sub of one.
// Use it where the migrations are not on disk; the file-writing operations
// (Init, CommitCurrentMigration, RevertLastMigration) reject it.
func WithCommittedFS(fsys fs.FS) Option {
	return func(c *config) {
		c.committedFS = fsys
		c.committedDir = ""
	}
}

// newConfig applies opts.
func newConfig(opts []Option) config {
	var c config
	for _, opt := range opts {
		opt(&c)
	}
	return c
}

// fsys returns the filesystem holding numbered migrations, rooted at the
// committed directory. root is the migrations directory, used when no
// committed location is configured.
func (c config) fsys(root fs.FS) (fs.FS, error) {
	switch {
	case c.committedFS != nil:
		return c.committedFS, nil
	case c.committedDir != "":
		// Check the directory here: an os.DirFS rooted at a missing path only
		// fails later, reporting the path as ".", which tells nobody which
		// directory was wrong.
		info, err := os.Stat(c.committedDir)
		if err != nil {
			return nil, fmt.Errorf("committed migrations directory: %w", err)
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("committed migrations directory %s is not a directory", c.committedDir)
		}
		return os.DirFS(c.committedDir), nil
	default:
		return root, nil
	}
}

// path returns the directory holding numbered migrations for operations that
// write files. migrationsDir is used when no committed directory is set.
func (c config) path(migrationsDir string) (string, error) {
	if c.committedFS != nil {
		return "", fmt.Errorf("committed migrations were supplied as a filesystem: use WithCommittedDir for operations that write files")
	}
	if c.committedDir != "" {
		return c.committedDir, nil
	}
	return migrationsDir, nil
}
