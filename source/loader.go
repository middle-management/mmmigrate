package source

import (
	"errors"
	"fmt"
	"io/fs"
	"strconv"
	"strings"
)

// CurrentFile is the name of the development migration file, always read from
// the root of the migrations directory.
const CurrentFile = "current.sql"

// LoadMigrations reads all .sql files from the migrations FS rooted at ".".
// current.sql is read from the root; numbered migrations come from the
// subdirectory named by WithCommittedDir, or from the root if none is set.
func LoadMigrations(fsys fs.FS, loadCurrent bool, opts ...Option) ([]*Migration, error) {
	cfg, err := newConfig(opts)
	if err != nil {
		return nil, err
	}

	var migrations []*Migration

	if loadCurrent {
		content, err := fs.ReadFile(fsys, CurrentFile)
		switch {
		case err == nil:
			migrations = append(migrations, &Migration{
				Version:   -1,
				Name:      "current",
				Filename:  CurrentFile,
				SQL:       string(content),
				IsCurrent: true,
			})
		case errors.Is(err, fs.ErrNotExist):
			// Nothing to apply yet — the user may not have created it.
		default:
			return nil, fmt.Errorf("failed to read current.sql: %w", err)
		}
	}

	dir := cfg.dir()
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, fmt.Errorf("failed to read migrations directory: %w", err)
	}

	seen := make(map[int]string) // version -> filename, for duplicate detection

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") || entry.Name() == CurrentFile {
			continue
		}

		version, name, err := ParseMigrationName(entry.Name())
		if err != nil {
			return nil, fmt.Errorf("failed to parse migration name %s: %w", entry.Name(), err)
		}

		if existing, ok := seen[version]; ok {
			return nil, fmt.Errorf("duplicate migration version %d: %s and %s", version, existing, entry.Name())
		}
		seen[version] = entry.Name()

		content, err := fs.ReadFile(fsys, cfg.join(entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("failed to read migration file %s: %w", entry.Name(), err)
		}

		migrations = append(migrations, &Migration{
			Version:  version,
			Name:     name,
			Filename: entry.Name(),
			SQL:      string(content),
		})
	}

	return migrations, nil
}

// ParseMigrationName extracts version and name from a migration filename.
// Format: "001_initial_schema.sql" -> version=1, name="initial_schema".
// A hyphen separates just as well, so Graphile Migrate's committed filenames
// ("000001-initial-schema.sql" -> version=1, name="initial-schema") parse
// without renaming.
func ParseMigrationName(filename string) (int, string, error) {
	name := strings.TrimSuffix(filename, ".sql")

	sep := strings.IndexAny(name, "_-")
	if sep < 0 {
		return 0, "", fmt.Errorf("invalid migration filename format: %s (expected: NNN_name.sql or NNN-name.sql)", filename)
	}

	version, err := strconv.Atoi(name[:sep])
	if err != nil {
		return 0, "", fmt.Errorf("invalid version number in filename %s: %w", filename, err)
	}

	return version, name[sep+1:], nil
}
