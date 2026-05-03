package source

import (
	"crypto/sha256"
	_ "embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

//go:embed current.sql.template
var emptyCurrentSQLTemplate string

// Init creates the migrations directory and an empty current.sql file.
func Init(migrationsDir string) error {
	if err := os.MkdirAll(migrationsDir, 0755); err != nil {
		return fmt.Errorf("failed to create migrations directory: %w", err)
	}

	currentPath := filepath.Join(migrationsDir, "current.sql")
	if _, err := os.Stat(currentPath); err == nil {
		return fmt.Errorf("migrations directory already initialized (%s exists)", currentPath)
	}

	if err := os.WriteFile(currentPath, []byte(emptyCurrentSQLTemplate), 0644); err != nil {
		return fmt.Errorf("failed to write current.sql: %w", err)
	}

	return nil
}

// Render expands @include directives in current.sql and returns the result.
func Render(fsys fs.FS) (string, error) {
	content, err := fs.ReadFile(fsys, "current.sql")
	if err != nil {
		return "", fmt.Errorf("failed to read current.sql: %w", err)
	}

	processed, _, err := ProcessIncludes(string(content), fsys)
	if err != nil {
		return "", fmt.Errorf("failed to process includes: %w", err)
	}

	return processed, nil
}

// CommitCurrentMigration converts current.sql to a numbered migration file.
func CommitCurrentMigration(migrationsDir string, description string) error {
	currentPath := filepath.Join(migrationsDir, "current.sql")

	content, err := os.ReadFile(currentPath)
	if err != nil {
		return fmt.Errorf("failed to read current.sql: %w", err)
	}

	if strings.TrimSpace(string(content)) == "" {
		return fmt.Errorf("current.sql is empty, nothing to commit")
	}

	lines := strings.Split(string(content), "\n")
	hasRealSQL := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" && !strings.HasPrefix(trimmed, "--") {
			hasRealSQL = true
			break
		}
	}

	if !hasRealSQL {
		return fmt.Errorf("current.sql contains only comments, nothing to commit")
	}

	processedContent, includeInfos, err := ProcessIncludes(string(content), os.DirFS(migrationsDir))
	if err != nil {
		return fmt.Errorf("failed to process includes: %w", err)
	}

	contentChecksum := fmt.Sprintf("%x", sha256.Sum256([]byte(processedContent)))

	// Compute chain hash from existing migrations.
	prevChain, err := getLastChainHash(migrationsDir)
	if err != nil {
		return fmt.Errorf("failed to read chain: %w", err)
	}
	chainHash := computeChainHash(prevChain, contentChecksum)

	header := buildMigrationHeader(description, contentChecksum, chainHash, includeInfos)
	finalContent := header + "\n" + processedContent

	nextVersion, err := getNextMigrationVersion(migrationsDir)
	if err != nil {
		return fmt.Errorf("failed to determine next migration version: %w", err)
	}

	filename := fmt.Sprintf("%03d_%s.sql", nextVersion, sanitizeDescription(description))
	newPath := filepath.Join(migrationsDir, filename)

	// Write atomically via temp file + rename.
	if err := atomicWriteFile(newPath, []byte(finalContent)); err != nil {
		return fmt.Errorf("failed to write migration file: %w", err)
	}

	if err := atomicWriteFile(currentPath, []byte(emptyCurrentSQLTemplate)); err != nil {
		return fmt.Errorf("failed to clear current.sql: %w", err)
	}

	fmt.Printf("✓ Committed current.sql as %s\n", filename)
	fmt.Printf("✓ Cleared current.sql\n")

	return nil
}

// atomicWriteFile writes data to a temp file then renames it into place.
func atomicWriteFile(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// CheckDirtyCurrent returns an error if current.sql contains uncommitted changes.
func CheckDirtyCurrent(migrationsDir string) error {
	currentPath := filepath.Join(migrationsDir, "current.sql")

	content, err := os.ReadFile(currentPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("failed to read current.sql: %w", err)
	}

	lines := strings.Split(string(content), "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" && !strings.HasPrefix(trimmed, "--") {
			return fmt.Errorf("current.sql contains uncommitted changes - commit before merging")
		}
	}

	return nil
}

// RevertLastMigration converts the last committed migration back to current.sql,
// restoring @include directives from the compiled include markers.
func RevertLastMigration(migrationsDir string) error {
	// Ensure current.sql is clean before reverting.
	if err := CheckDirtyCurrent(migrationsDir); err != nil {
		return fmt.Errorf("cannot revert: %w", err)
	}

	files, err := listMigrationFiles(migrationsDir)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return fmt.Errorf("no migrations to revert")
	}

	last := files[len(files)-1]
	path := filepath.Join(migrationsDir, last.filename)

	content, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("failed to read %s: %w", last.filename, err)
	}

	body := extractBody(string(content))
	restored := decompileIncludes(body)

	currentPath := filepath.Join(migrationsDir, "current.sql")
	if err := atomicWriteFile(currentPath, []byte(restored)); err != nil {
		return fmt.Errorf("failed to write current.sql: %w", err)
	}

	if err := os.Remove(path); err != nil {
		return fmt.Errorf("failed to remove %s: %w", last.filename, err)
	}

	fmt.Printf("✓ Reverted %s to current.sql\n", last.filename)
	return nil
}

var beginIncludePrefix = "-- BEGIN INCLUDE: "
var endIncludePrefix = "-- END INCLUDE: "

// decompileIncludes replaces compiled include blocks with @include directives.
func decompileIncludes(body string) string {
	lines := strings.Split(body, "\n")
	var result []string

	for i := 0; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])

		if !strings.HasPrefix(trimmed, beginIncludePrefix) {
			result = append(result, lines[i])
			continue
		}

		// Extract the include path from "-- BEGIN INCLUDE: path [checksum: ...]"
		rest := strings.TrimPrefix(trimmed, beginIncludePrefix)
		path := rest
		if idx := strings.Index(rest, " ["); idx >= 0 {
			path = rest[:idx]
		}

		result = append(result, "-- @include "+path)

		// Skip lines until the matching END INCLUDE.
		endMarker := endIncludePrefix + path
		for i++; i < len(lines); i++ {
			if strings.TrimSpace(lines[i]) == endMarker {
				break
			}
		}
	}

	return strings.Join(result, "\n")
}

// migrationHeaders holds values extracted from a migration file header.
type migrationHeaders struct {
	Checksum  string
	Chain     string
	HasHeader bool // true if any migration header lines (Migration:, Created:) were found
}

// extractHeaders parses the comment header of a migration file.
func extractHeaders(content string) migrationHeaders {
	var h migrationHeaders
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)

		switch {
		case strings.HasPrefix(trimmed, "-- Migration:"), strings.HasPrefix(trimmed, "-- Created:"):
			h.HasHeader = true
		case strings.HasPrefix(trimmed, "-- Checksum:"):
			h.HasHeader = true
			if parts := strings.SplitN(trimmed, ":", 2); len(parts) == 2 {
				h.Checksum = strings.TrimSpace(parts[1])
			}
		case strings.HasPrefix(trimmed, "-- Chain:"):
			if parts := strings.SplitN(trimmed, ":", 2); len(parts) == 2 {
				h.Chain = strings.TrimSpace(parts[1])
			}
		case trimmed != "" && !strings.HasPrefix(trimmed, "--"):
			return h // end of header
		}
	}
	return h
}

// extractBody returns the migration content after the comment header.
func extractBody(content string) string {
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" && !strings.HasPrefix(trimmed, "--") {
			return strings.Join(lines[i:], "\n")
		}
	}
	return ""
}

// ValidateMigrationIntegrity checks if a migration file's content matches its checksum.
func ValidateMigrationIntegrity(filePath string) error {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("failed to read migration file: %w", err)
	}

	h := extractHeaders(string(content))

	if h.Checksum == "" {
		if h.HasHeader {
			return fmt.Errorf("migration has headers but missing checksum")
		}
		return nil // plain SQL file without migration headers
	}

	body := extractBody(string(content))
	actual := fmt.Sprintf("%x", sha256.Sum256([]byte(body)))

	if h.Checksum != actual {
		return fmt.Errorf("checksum mismatch: expected %s, got %s", h.Checksum, actual)
	}

	return nil
}

// ValidateChain walks all numbered migrations in order and verifies both
// content checksums and the merkle chain.
func ValidateChain(migrationsDir string) error {
	files, err := listMigrationFiles(migrationsDir)
	if err != nil {
		return err
	}

	prevChain := ""
	for _, f := range files {
		content, err := os.ReadFile(filepath.Join(migrationsDir, f.filename))
		if err != nil {
			return fmt.Errorf("%s: %w", f.filename, err)
		}

		h := extractHeaders(string(content))
		if !h.HasHeader {
			continue // plain SQL without migration headers, skip
		}

		if h.Checksum == "" {
			return fmt.Errorf("%s: has headers but missing checksum", f.filename)
		}

		// Verify content checksum.
		body := extractBody(string(content))
		actual := fmt.Sprintf("%x", sha256.Sum256([]byte(body)))
		if h.Checksum != actual {
			return fmt.Errorf("%s: checksum mismatch (expected %s, got %s)", f.filename, h.Checksum, actual)
		}

		// Verify chain.
		if h.Chain == "" {
			// Migration predates chain feature — reset and continue.
			prevChain = ""
			continue
		}

		expected := computeChainHash(prevChain, h.Checksum)
		if h.Chain != expected {
			return fmt.Errorf("%s: chain integrity failed (expected %s, got %s)", f.filename, expected, h.Chain)
		}
		prevChain = h.Chain
	}

	return nil
}

type migFile struct {
	version  int
	filename string
}

func listMigrationFiles(migrationsDir string) ([]migFile, error) {
	entries, err := os.ReadDir(migrationsDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read migrations directory: %w", err)
	}

	var files []migFile
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") || entry.Name() == "current.sql" {
			continue
		}
		version, _, err := ParseMigrationName(entry.Name())
		if err != nil {
			continue
		}
		files = append(files, migFile{version, entry.Name()})
	}

	sort.Slice(files, func(i, j int) bool { return files[i].version < files[j].version })
	return files, nil
}

func computeChainHash(previousChain, contentChecksum string) string {
	h := sha256.Sum256([]byte(previousChain + contentChecksum))
	return fmt.Sprintf("%x", h)
}

func getLastChainHash(migrationsDir string) (string, error) {
	files, err := listMigrationFiles(migrationsDir)
	if err != nil {
		return "", err
	}

	if len(files) == 0 {
		return "", nil
	}

	last := files[len(files)-1]
	content, err := os.ReadFile(filepath.Join(migrationsDir, last.filename))
	if err != nil {
		return "", fmt.Errorf("failed to read %s: %w", last.filename, err)
	}

	h := extractHeaders(string(content))
	return h.Chain, nil // empty string if no chain header (pre-chain migration)
}

func getNextMigrationVersion(migrationsDir string) (int, error) {
	files, err := listMigrationFiles(migrationsDir)
	if err != nil {
		return 0, err
	}

	if len(files) == 0 {
		return 1, nil
	}
	return files[len(files)-1].version + 1, nil
}

func sanitizeDescription(description string) string {
	result := strings.ToLower(description)
	result = strings.ReplaceAll(result, " ", "_")
	result = strings.ReplaceAll(result, "-", "_")

	var clean strings.Builder
	for _, r := range result {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' {
			clean.WriteRune(r)
		}
	}

	cleaned := strings.Trim(clean.String(), "_")
	for strings.Contains(cleaned, "__") {
		cleaned = strings.ReplaceAll(cleaned, "__", "_")
	}

	if cleaned == "" {
		cleaned = "migration"
	}

	return cleaned
}

func buildMigrationHeader(description, contentChecksum, chainHash string, includeInfos []IncludeInfo) string {
	var header strings.Builder
	now := time.Now().UTC()

	header.WriteString(fmt.Sprintf("-- Migration: %s\n", description))
	header.WriteString(fmt.Sprintf("-- Created: %s\n", now.Format(time.RFC3339)))
	header.WriteString(fmt.Sprintf("-- Checksum: %s\n", contentChecksum))
	header.WriteString(fmt.Sprintf("-- Chain: %s\n", chainHash))
	header.WriteString("--\n")
	header.WriteString("-- IMPORTANT: Do not modify this file after commit. The checksum above\n")
	header.WriteString("-- tracks the integrity of this migration. Any changes will be detected\n")
	header.WriteString("-- and may cause deployment issues.\n")

	if len(includeInfos) > 0 {
		header.WriteString("--\n")
		header.WriteString("-- This migration was compiled from current.sql with includes:\n")
		for _, info := range includeInfos {
			header.WriteString(fmt.Sprintf("--   - %s (lines %d-%d) [%s]\n",
				info.Path, info.StartLine, info.EndLine, info.Checksum))
		}
	}

	header.WriteString("--\n")

	return header.String()
}
