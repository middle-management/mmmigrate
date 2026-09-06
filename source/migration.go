package source

// Migration represents a single migration loaded from the filesystem.
type Migration struct {
	Version int
	Name    string
	// Filename is the base name of the file this migration was read from,
	// which may differ from the canonical NNN_name.sql form.
	Filename  string
	SQL       string
	IsCurrent bool
}
