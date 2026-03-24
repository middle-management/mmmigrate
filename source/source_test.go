package source_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/middle-management/mmmigrate/source"
)

func writeSQL(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

// commitMigration is a helper that writes current.sql and commits it.
func commitMigration(t *testing.T, dir, sql, desc string) {
	t.Helper()
	writeSQL(t, dir, "current.sql", sql)
	if err := source.CommitCurrentMigration(dir, desc); err != nil {
		t.Fatal(err)
	}
}

// --- ParseMigrationName ---

func TestParseMigrationName(t *testing.T) {
	tests := []struct {
		filename    string
		wantVersion int
		wantName    string
		wantErr     bool
	}{
		{"001_initial_schema.sql", 1, "initial_schema", false},
		{"042_add_users.sql", 42, "add_users", false},
		{"bad.sql", 0, "", true},
		{"abc_name.sql", 0, "", true},
	}

	for _, tt := range tests {
		v, n, err := source.ParseMigrationName(tt.filename)
		if (err != nil) != tt.wantErr {
			t.Errorf("ParseMigrationName(%q): err=%v, wantErr=%v", tt.filename, err, tt.wantErr)
			continue
		}
		if v != tt.wantVersion || n != tt.wantName {
			t.Errorf("ParseMigrationName(%q) = (%d, %q), want (%d, %q)", tt.filename, v, n, tt.wantVersion, tt.wantName)
		}
	}
}

// --- LoadMigrations ---

func TestLoadMigrations(t *testing.T) {
	dir := t.TempDir()

	writeSQL(t, dir, "001_first.sql", "SELECT 1;")
	writeSQL(t, dir, "002_second.sql", "SELECT 2;")
	writeSQL(t, dir, "current.sql", "SELECT 3;")

	migs, err := source.LoadMigrations(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(migs) != 2 {
		t.Errorf("expected 2 migrations without current, got %d", len(migs))
	}

	migs, err = source.LoadMigrations(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(migs) != 3 {
		t.Errorf("expected 3 migrations with current, got %d", len(migs))
	}
}

func TestLoadMigrationsDuplicateVersion(t *testing.T) {
	dir := t.TempDir()

	writeSQL(t, dir, "001_first.sql", "SELECT 1;")
	writeSQL(t, dir, "001_second.sql", "SELECT 2;")

	_, err := source.LoadMigrations(dir, false)
	if err == nil {
		t.Fatal("expected error for duplicate version")
	}
	if !strings.Contains(err.Error(), "duplicate migration version") {
		t.Errorf("expected duplicate version error, got: %v", err)
	}
}

// --- CommitCurrentMigration ---

func TestCommitCurrentMigration(t *testing.T) {
	dir := t.TempDir()

	writeSQL(t, dir, "current.sql", `CREATE TABLE orders (id INTEGER PRIMARY KEY);`)

	if err := source.CommitCurrentMigration(dir, "create orders table"); err != nil {
		t.Fatal(err)
	}

	entries, _ := os.ReadDir(dir)
	var found bool
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "001_") && strings.HasSuffix(e.Name(), ".sql") {
			found = true
			content, _ := os.ReadFile(filepath.Join(dir, e.Name()))
			s := string(content)
			if !strings.Contains(s, "CREATE TABLE orders") {
				t.Error("committed migration should contain the original SQL")
			}
			if !strings.Contains(s, "-- Checksum:") {
				t.Error("committed migration should contain a checksum header")
			}
			if !strings.Contains(s, "-- Chain:") {
				t.Error("committed migration should contain a chain header")
			}
		}
		// No temp files left behind.
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("temp file left behind: %s", e.Name())
		}
	}
	if !found {
		t.Error("expected numbered migration file to be created")
	}

	current, _ := os.ReadFile(filepath.Join(dir, "current.sql"))
	if strings.Contains(string(current), "CREATE TABLE") {
		t.Error("current.sql should be cleared after commit")
	}
}

func TestCommitIncrementsVersion(t *testing.T) {
	dir := t.TempDir()

	writeSQL(t, dir, "001_first.sql", `CREATE TABLE a (id INTEGER);`)
	writeSQL(t, dir, "002_second.sql", `CREATE TABLE b (id INTEGER);`)
	writeSQL(t, dir, "current.sql", `CREATE TABLE c (id INTEGER);`)

	if err := source.CommitCurrentMigration(dir, "third migration"); err != nil {
		t.Fatal(err)
	}

	entries, _ := os.ReadDir(dir)
	var found bool
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "003_") {
			found = true
		}
	}
	if !found {
		t.Error("expected migration version 003")
	}
}

// --- CheckDirtyCurrent ---

func TestCheckDirtyCurrent(t *testing.T) {
	dir := t.TempDir()

	writeSQL(t, dir, "current.sql", "-- nothing here\n")
	if err := source.CheckDirtyCurrent(dir); err != nil {
		t.Errorf("expected clean current.sql: %v", err)
	}

	writeSQL(t, dir, "current.sql", "ALTER TABLE users ADD COLUMN x TEXT;\n")
	if err := source.CheckDirtyCurrent(dir); err == nil {
		t.Error("expected error for dirty current.sql")
	}
}

func TestCheckDirtyCurrentMissingFile(t *testing.T) {
	dir := t.TempDir()
	if err := source.CheckDirtyCurrent(dir); err != nil {
		t.Errorf("expected no error when current.sql is missing: %v", err)
	}
}

// --- ValidateMigrationIntegrity ---

func TestValidateMigrationIntegrity(t *testing.T) {
	dir := t.TempDir()

	commitMigration(t, dir, `CREATE TABLE valid (id INTEGER);`, "valid migration")

	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), "001_") {
			continue
		}

		path := filepath.Join(dir, e.Name())

		if err := source.ValidateMigrationIntegrity(path); err != nil {
			t.Errorf("expected valid integrity: %v", err)
		}

		// Tamper with content.
		content, _ := os.ReadFile(path)
		tampered := strings.Replace(string(content), "CREATE TABLE valid", "CREATE TABLE tampered", 1)
		os.WriteFile(path, []byte(tampered), 0644)

		if err := source.ValidateMigrationIntegrity(path); err == nil {
			t.Error("expected integrity check to fail after tampering")
		}
	}
}

func TestValidateMigrationIntegrityMissingChecksum(t *testing.T) {
	dir := t.TempDir()

	// A file with migration headers but no Checksum line.
	writeSQL(t, dir, "001_bad.sql", "-- Migration: bad\n-- Created: 2026-01-01\n--\nSELECT 1;\n")

	err := source.ValidateMigrationIntegrity(filepath.Join(dir, "001_bad.sql"))
	if err == nil {
		t.Error("expected error for missing checksum with headers present")
	}
	if !strings.Contains(err.Error(), "missing checksum") {
		t.Errorf("expected missing checksum error, got: %v", err)
	}
}

func TestValidatePlainSQLFileOK(t *testing.T) {
	dir := t.TempDir()

	// A plain SQL file with no migration headers should pass.
	writeSQL(t, dir, "001_plain.sql", "CREATE TABLE foo (id INTEGER);\n")

	if err := source.ValidateMigrationIntegrity(filepath.Join(dir, "001_plain.sql")); err != nil {
		t.Errorf("plain SQL file should pass validation: %v", err)
	}
}

// --- Include processing ---

func TestIncludeProcessing(t *testing.T) {
	dir := t.TempDir()

	funcDir := filepath.Join(dir, "functions")
	os.MkdirAll(funcDir, 0755)
	writeSQL(t, funcDir, "helper.sql", `SELECT 1;`)

	result, infos, err := source.ProcessIncludes("-- @include functions/helper.sql", dir)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(result, "SELECT 1;") {
		t.Error("expected included content in result")
	}
	if !strings.Contains(result, "-- BEGIN INCLUDE:") {
		t.Error("expected include boundary marker")
	}
	if len(infos) != 1 {
		t.Errorf("expected 1 include info, got %d", len(infos))
	}
}

func TestIncludeCircularDetection(t *testing.T) {
	dir := t.TempDir()

	writeSQL(t, dir, "a.sql", "-- @include b.sql")
	writeSQL(t, dir, "b.sql", "-- @include a.sql")

	_, _, err := source.ProcessIncludes("-- @include a.sql", dir)
	if err == nil {
		t.Error("expected error for circular include")
	}
	if !strings.Contains(err.Error(), "circular") {
		t.Errorf("expected circular include error, got: %v", err)
	}
}

func TestIncludePathTraversal(t *testing.T) {
	dir := t.TempDir()

	_, _, err := source.ProcessIncludes("-- @include ../../../etc/passwd", dir)
	if err == nil {
		t.Fatal("expected error for path traversal")
	}
	if !strings.Contains(err.Error(), "escapes base directory") {
		t.Errorf("expected path traversal error, got: %v", err)
	}
}

// --- Merkle chain ---

func TestChainValidation(t *testing.T) {
	dir := t.TempDir()

	commitMigration(t, dir, `CREATE TABLE a (id INTEGER);`, "first")
	commitMigration(t, dir, `CREATE TABLE b (id INTEGER);`, "second")
	commitMigration(t, dir, `CREATE TABLE c (id INTEGER);`, "third")

	// Chain should validate.
	if err := source.ValidateChain(dir); err != nil {
		t.Fatalf("expected valid chain: %v", err)
	}
}

func TestChainDetectsTamperingFirst(t *testing.T) {
	dir := t.TempDir()

	commitMigration(t, dir, `CREATE TABLE a (id INTEGER);`, "first")
	commitMigration(t, dir, `CREATE TABLE b (id INTEGER);`, "second")

	// Tamper with the first migration's body.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "001_") {
			path := filepath.Join(dir, e.Name())
			content, _ := os.ReadFile(path)
			tampered := strings.Replace(string(content), "CREATE TABLE a", "CREATE TABLE hacked", 1)
			os.WriteFile(path, []byte(tampered), 0644)
		}
	}

	err := source.ValidateChain(dir)
	if err == nil {
		t.Fatal("expected chain validation to fail after tampering")
	}
	if !strings.Contains(err.Error(), "001_") {
		t.Errorf("expected error to reference first migration, got: %v", err)
	}
}

func TestChainDetectsTamperingMiddle(t *testing.T) {
	dir := t.TempDir()

	commitMigration(t, dir, `CREATE TABLE a (id INTEGER);`, "first")
	commitMigration(t, dir, `CREATE TABLE b (id INTEGER);`, "second")
	commitMigration(t, dir, `CREATE TABLE c (id INTEGER);`, "third")

	// Tamper with the second migration. The checksum still matches (we update it)
	// but the chain should break at migration 3.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), "002_") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		content, _ := os.ReadFile(path)
		s := string(content)
		// Replace the body and fix the checksum to match, but the chain will break.
		s = strings.Replace(s, "CREATE TABLE b", "CREATE TABLE hacked", 1)
		os.WriteFile(path, []byte(s), 0644)
	}

	err := source.ValidateChain(dir)
	if err == nil {
		t.Fatal("expected chain validation to fail")
	}
}

// --- Revert ---

func TestRevertLastMigration(t *testing.T) {
	dir := t.TempDir()

	commitMigration(t, dir, `CREATE TABLE a (id INTEGER);`, "first")
	commitMigration(t, dir, `CREATE TABLE b (id INTEGER);`, "second")

	if err := source.RevertLastMigration(dir); err != nil {
		t.Fatal(err)
	}

	// current.sql should contain the reverted content.
	current, _ := os.ReadFile(filepath.Join(dir, "current.sql"))
	if !strings.Contains(string(current), "CREATE TABLE b") {
		t.Error("current.sql should contain the reverted migration SQL")
	}

	// 002 should be gone.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "002_") {
			t.Error("002 migration file should be deleted after revert")
		}
	}

	// 001 should still exist.
	var has001 bool
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "001_") {
			has001 = true
		}
	}
	if !has001 {
		t.Error("001 migration should still exist")
	}
}

func TestRevertRestoresIncludes(t *testing.T) {
	dir := t.TempDir()

	// Create an include file and a current.sql that uses it.
	funcDir := filepath.Join(dir, "functions")
	os.MkdirAll(funcDir, 0755)
	writeSQL(t, funcDir, "helper.sql", "CREATE TABLE helper (id INTEGER);")

	writeSQL(t, dir, "current.sql", "CREATE TABLE main (id INTEGER);\n-- @include functions/helper.sql\n")
	if err := source.CommitCurrentMigration(dir, "with includes"); err != nil {
		t.Fatal(err)
	}

	if err := source.RevertLastMigration(dir); err != nil {
		t.Fatal(err)
	}

	current, _ := os.ReadFile(filepath.Join(dir, "current.sql"))
	s := string(current)

	if !strings.Contains(s, "-- @include functions/helper.sql") {
		t.Error("reverted current.sql should restore @include directives")
	}
	if strings.Contains(s, "-- BEGIN INCLUDE:") {
		t.Error("reverted current.sql should not contain compiled include markers")
	}
	if strings.Contains(s, "CREATE TABLE helper") {
		t.Error("reverted current.sql should not contain inlined include content")
	}
	if !strings.Contains(s, "CREATE TABLE main") {
		t.Error("reverted current.sql should contain non-included SQL")
	}
}

func TestRevertFailsWithDirtyCurrent(t *testing.T) {
	dir := t.TempDir()

	commitMigration(t, dir, `CREATE TABLE a (id INTEGER);`, "first")

	// Make current.sql dirty.
	writeSQL(t, dir, "current.sql", "ALTER TABLE a ADD COLUMN x TEXT;")

	err := source.RevertLastMigration(dir)
	if err == nil {
		t.Fatal("expected error when current.sql is dirty")
	}
	if !strings.Contains(err.Error(), "cannot revert") {
		t.Errorf("expected 'cannot revert' error, got: %v", err)
	}
}

func TestRevertFailsWithNoMigrations(t *testing.T) {
	dir := t.TempDir()

	err := source.RevertLastMigration(dir)
	if err == nil {
		t.Fatal("expected error with no migrations")
	}
	if !strings.Contains(err.Error(), "no migrations to revert") {
		t.Errorf("expected 'no migrations' error, got: %v", err)
	}
}

func TestChainEmptyDir(t *testing.T) {
	dir := t.TempDir()

	if err := source.ValidateChain(dir); err != nil {
		t.Fatalf("expected no error for empty dir: %v", err)
	}
}
