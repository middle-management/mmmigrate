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

func TestCommitCurrentMigration(t *testing.T) {
	dir := t.TempDir()

	writeSQL(t, dir, "current.sql", `CREATE TABLE orders (id INTEGER PRIMARY KEY);`)
	writeSQL(t, dir, "current.sql.template", "-- empty\n")

	if err := source.CommitCurrentMigration(dir, "create orders table"); err != nil {
		t.Fatal(err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}

	var found bool
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "001_") && strings.HasSuffix(e.Name(), ".sql") {
			found = true
			content, _ := os.ReadFile(filepath.Join(dir, e.Name()))
			if !strings.Contains(string(content), "CREATE TABLE orders") {
				t.Error("committed migration should contain the original SQL")
			}
			if !strings.Contains(string(content), "-- Checksum:") {
				t.Error("committed migration should contain a checksum header")
			}
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

func TestValidateMigrationIntegrity(t *testing.T) {
	dir := t.TempDir()

	writeSQL(t, dir, "current.sql", `CREATE TABLE valid (id INTEGER);`)
	if err := source.CommitCurrentMigration(dir, "valid migration"); err != nil {
		t.Fatal(err)
	}

	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), "001_") {
			continue
		}

		path := filepath.Join(dir, e.Name())

		if err := source.ValidateMigrationIntegrity(path); err != nil {
			t.Errorf("expected valid integrity: %v", err)
		}

		content, _ := os.ReadFile(path)
		tampered := strings.Replace(string(content), "CREATE TABLE valid", "CREATE TABLE tampered", 1)
		os.WriteFile(path, []byte(tampered), 0644)

		if err := source.ValidateMigrationIntegrity(path); err == nil {
			t.Error("expected integrity check to fail after tampering")
		}
	}
}

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
