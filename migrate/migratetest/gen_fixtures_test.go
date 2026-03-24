package migratetest_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/middle-management/mmmigrate/source"
)

// TestGenerateFixtures rebuilds the testdata/migrations directory by running
// each migration through CommitCurrentMigration, producing real headers with
// checksums and a merkle chain.
//
// Run with: go test -run TestGenerateFixtures -generate-fixtures
func TestGenerateFixtures(t *testing.T) {
	if os.Getenv("GENERATE_FIXTURES") == "" {
		t.Skip("set GENERATE_FIXTURES=1 to regenerate fixture migrations")
	}

	dir := filepath.Join("testdata", "migrations")

	// Wipe numbered migrations but keep functions/ and current.sql.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if !e.IsDir() && e.Name() != "current.sql" {
			os.Remove(filepath.Join(dir, e.Name()))
		}
	}

	steps := []struct {
		sql  string
		desc string
	}{
		{
			sql:  "CREATE TABLE users (\n    id INTEGER PRIMARY KEY,\n    name TEXT NOT NULL,\n    email TEXT\n);\n",
			desc: "create users",
		},
		{
			sql:  "CREATE TABLE posts (\n    id INTEGER PRIMARY KEY,\n    user_id INTEGER NOT NULL,\n    title TEXT NOT NULL,\n    body TEXT\n);\n",
			desc: "create posts",
		},
		{
			sql:  "ALTER TABLE posts ADD COLUMN published_at TEXT;\n",
			desc: "add published_at",
		},
	}

	for _, s := range steps {
		if err := os.WriteFile(filepath.Join(dir, "current.sql"), []byte(s.sql), 0644); err != nil {
			t.Fatal(err)
		}
		if err := source.CommitCurrentMigration(dir, s.desc); err != nil {
			t.Fatal(err)
		}
	}

	// Restore current.sql with the include directive for dev-mode tests.
	if err := os.WriteFile(filepath.Join(dir, "current.sql"), []byte("-- @include functions/add_bio.sql\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Verify the chain is valid.
	if err := source.ValidateChain(dir); err != nil {
		t.Fatalf("generated fixtures have invalid chain: %v", err)
	}

	t.Log("fixtures regenerated and chain validated")
}
