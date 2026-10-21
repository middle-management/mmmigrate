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

// TestCommitShadowBaselineFromEnv checks that baselines named in
// MMMIGRATE_SHADOW_BASELINE are loaded into the shadow before the replay, in
// order, so current.sql can use objects it doesn't create.
func TestCommitShadowBaselineFromEnv(t *testing.T) {
	tmp := t.TempDir()
	dir := filepath.Join(tmp, "migrations")
	if err := os.Mkdir(dir, 0755); err != nil {
		t.Fatal(err)
	}

	// Two baselines, the second depending on the first.
	first := filepath.Join(tmp, "first.sql")
	second := filepath.Join(tmp, "second.sql")
	if err := os.WriteFile(first, []byte("CREATE TABLE owned (id INTEGER PRIMARY KEY);\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(second, []byte("INSERT INTO owned (id) VALUES (1);\n"), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MMMIGRATE_SHADOW_BASELINE", first+string(os.PathListSeparator)+second)

	// The dev database already has the other app's table.
	db, err := sql.Open(dialect.DriverName(), "file:"+filepath.Join(tmp, "dev.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("CREATE TABLE owned (id INTEGER PRIMARY KEY); INSERT INTO owned (id) VALUES (1)"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "current.sql"), []byte("UPDATE owned SET id = 2 WHERE id = 1;\nINSERT INTO owned (id) VALUES (3);\n"), 0644); err != nil {
		t.Fatal(err)
	}

	shadowURL := "file:" + filepath.Join(tmp, "shadow.db")
	if err := commit(context.Background(), db, shadowURL, dir, "use owned", false); err == nil {
		t.Fatal("commit passed shadow verification without the baselines")
	}

	opts, err := shadowBaselines(nil, shadowURL)
	if err != nil {
		t.Fatal(err)
	}
	if len(opts) != 2 {
		t.Fatalf("expected 2 baselines from the env var, got %d", len(opts))
	}
	if err := commit(context.Background(), db, shadowURL, dir, "use owned", false, opts...); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "001_use_owned.sql")); err != nil {
		t.Errorf("expected 001_use_owned.sql: %v", err)
	}
}

// TestShadowBaselinesWithoutShadowURL checks that an explicit
// -shadow-baseline needs a shadow URL, while the env var is then ignored.
func TestShadowBaselinesWithoutShadowURL(t *testing.T) {
	_, err := shadowBaselines([]string{"schema.sql"}, "")
	if err == nil || !strings.Contains(err.Error(), "requires -shadow-url") {
		t.Errorf("expected a requires -shadow-url error, got %v", err)
	}

	t.Setenv("MMMIGRATE_SHADOW_BASELINE", filepath.Join(t.TempDir(), "missing.sql"))
	opts, err := shadowBaselines(nil, "")
	if err != nil || len(opts) != 0 {
		t.Errorf("env baseline without a shadow URL: got %d options, err %v", len(opts), err)
	}
}

// TestShadowBaselinesMissingFile checks that a baseline that can't be read
// stops the commit before any database work.
func TestShadowBaselinesMissingFile(t *testing.T) {
	_, err := shadowBaselines([]string{filepath.Join(t.TempDir(), "missing.sql")}, "file:shadow.db")
	if err == nil || !strings.Contains(err.Error(), "failed to read shadow baseline") {
		t.Errorf("expected a read error, got %v", err)
	}
}
