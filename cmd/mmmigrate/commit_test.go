//go:build sqlite

package main

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCommitShadowFailureWritesNothing checks that a migration which fails
// shadow verification is not committed: no numbered file appears and
// current.sql is left as it was.
func TestCommitShadowFailureWritesNothing(t *testing.T) {
	tmp := t.TempDir()
	dir := filepath.Join(tmp, "migrations")
	if err := os.Mkdir(dir, 0755); err != nil {
		t.Fatal(err)
	}

	// The dev database has a table that no migration creates, so current.sql
	// passes against it but fails on a clean replay.
	db, err := sql.Open(dialect.DriverName(), "file:"+filepath.Join(tmp, "dev.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("CREATE TABLE handmade (id INTEGER)"); err != nil {
		t.Fatal(err)
	}

	current := "INSERT INTO handmade (id) VALUES (1);\n"
	if err := os.WriteFile(filepath.Join(dir, "current.sql"), []byte(current), 0644); err != nil {
		t.Fatal(err)
	}

	shadowURL := "file:" + filepath.Join(tmp, "shadow.db")
	err = commit(context.Background(), db, shadowURL, dir, "seed handmade", false)
	if err == nil || !strings.Contains(err.Error(), "shadow verification failed") {
		t.Fatalf("expected shadow verification error, got %v", err)
	}

	committed, err := filepath.Glob(filepath.Join(dir, "[0-9]*.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if len(committed) > 0 {
		t.Errorf("failed verification still committed %v", committed)
	}
	got, err := os.ReadFile(filepath.Join(dir, "current.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != current {
		t.Errorf("current.sql changed after failed verification:\n%s", got)
	}
}

// TestCommitShadowSuccess checks the happy path still commits.
func TestCommitShadowSuccess(t *testing.T) {
	tmp := t.TempDir()
	dir := filepath.Join(tmp, "migrations")
	if err := os.Mkdir(dir, 0755); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open(dialect.DriverName(), "file:"+filepath.Join(tmp, "dev.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if err := os.WriteFile(filepath.Join(dir, "current.sql"), []byte("CREATE TABLE t (id INTEGER);\n"), 0644); err != nil {
		t.Fatal(err)
	}

	shadowURL := "file:" + filepath.Join(tmp, "shadow.db")
	if err := commit(context.Background(), db, shadowURL, dir, "add t", false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "001_add_t.sql")); err != nil {
		t.Errorf("expected 001_add_t.sql: %v", err)
	}
}
