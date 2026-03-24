package source

// Migration represents a single migration loaded from the filesystem.
type Migration struct {
	Version   int
	Name      string
	SQL       string
	IsCurrent bool
}
