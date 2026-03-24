package source

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// LoadMigrations reads all .sql files from the migrations directory.
func LoadMigrations(dir string, loadCurrent bool) ([]*Migration, error) {
	var migrations []*Migration
	seen := make(map[int]string) // version -> filename, for duplicate detection

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("failed to read migrations directory: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}

		if entry.Name() == "current.sql" {
			if !loadCurrent {
				continue
			}

			content, err := os.ReadFile(filepath.Join(dir, entry.Name()))
			if err != nil {
				return nil, fmt.Errorf("failed to read current.sql: %w", err)
			}

			migrations = append(migrations, &Migration{
				Version:   -1,
				Name:      "current",
				SQL:       string(content),
				IsCurrent: true,
			})
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

		content, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("failed to read migration file %s: %w", entry.Name(), err)
		}

		migrations = append(migrations, &Migration{
			Version: version,
			Name:    name,
			SQL:     string(content),
		})
	}

	return migrations, nil
}

// ParseMigrationName extracts version and name from a migration filename.
// Format: "001_initial_schema.sql" -> version=1, name="initial_schema"
func ParseMigrationName(filename string) (int, string, error) {
	name := strings.TrimSuffix(filename, ".sql")

	parts := strings.SplitN(name, "_", 2)
	if len(parts) != 2 {
		return 0, "", fmt.Errorf("invalid migration filename format: %s (expected: NNN_name.sql)", filename)
	}

	version, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, "", fmt.Errorf("invalid version number in filename %s: %w", filename, err)
	}

	return version, parts[1], nil
}
