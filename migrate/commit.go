package migrate

import (
	"crypto/sha256"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

//go:embed current.sql.template
var emptyCurrentSQLTemplate string

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

	processedContent, includeInfos, err := processIncludes(string(content), migrationsDir)
	if err != nil {
		return fmt.Errorf("failed to process includes: %w", err)
	}

	contentChecksum := fmt.Sprintf("%x", sha256.Sum256([]byte(processedContent)))

	header := buildMigrationHeader(description, contentChecksum, includeInfos)
	finalContent := header + "\n" + processedContent

	nextVersion, err := getNextMigrationVersion(migrationsDir)
	if err != nil {
		return fmt.Errorf("failed to determine next migration version: %w", err)
	}

	filename := fmt.Sprintf("%03d_%s.sql", nextVersion, sanitizeDescription(description))
	newPath := filepath.Join(migrationsDir, filename)

	if err := os.WriteFile(newPath, []byte(finalContent), 0644); err != nil {
		return fmt.Errorf("failed to write new migration file: %w", err)
	}

	if err := os.WriteFile(currentPath, []byte(emptyCurrentSQLTemplate), 0644); err != nil {
		return fmt.Errorf("failed to clear current.sql: %w", err)
	}

	fmt.Printf("✓ Committed current.sql as %s\n", filename)
	fmt.Printf("✓ Cleared current.sql\n")

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

// ValidateMigrationIntegrity checks if a migration file's content matches its checksum.
func ValidateMigrationIntegrity(filePath string) error {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("failed to read migration file: %w", err)
	}

	lines := strings.Split(string(content), "\n")
	var expectedChecksum string
	var migrationContent strings.Builder

	headerEnd := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "-- Checksum:") {
			parts := strings.SplitN(trimmed, ":", 2)
			if len(parts) == 2 {
				expectedChecksum = strings.TrimSpace(parts[1])
			}
		}

		if !headerEnd && trimmed == "--" {
			headerEnd = true
			continue
		}

		if headerEnd {
			if i > 0 {
				migrationContent.WriteString("\n")
			}
			migrationContent.WriteString(line)
		}
	}

	if expectedChecksum == "" {
		return nil
	}

	actualChecksum := fmt.Sprintf("%x", sha256.Sum256([]byte(migrationContent.String())))

	if expectedChecksum != actualChecksum {
		return fmt.Errorf("migration integrity check failed: expected %s, got %s",
			expectedChecksum, actualChecksum)
	}

	return nil
}

func getNextMigrationVersion(migrationsDir string) (int, error) {
	entries, err := os.ReadDir(migrationsDir)
	if err != nil {
		return 0, fmt.Errorf("failed to read migrations directory: %w", err)
	}

	maxVersion := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") || entry.Name() == "current.sql" {
			continue
		}

		version, _, err := ParseMigrationName(entry.Name())
		if err != nil {
			continue
		}

		if version > maxVersion {
			maxVersion = version
		}
	}

	return maxVersion + 1, nil
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

func buildMigrationHeader(description string, contentChecksum string, includeInfos []IncludeInfo) string {
	var header strings.Builder
	now := time.Now().UTC()

	header.WriteString(fmt.Sprintf("-- Migration: %s\n", description))
	header.WriteString(fmt.Sprintf("-- Created: %s\n", now.Format(time.RFC3339)))
	header.WriteString(fmt.Sprintf("-- Checksum: %s\n", contentChecksum))
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
