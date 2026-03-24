// Package migratetest provides shared integration tests for driver packages.
package migratetest

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/middle-management/mmmigrate/migrate"
)

//go:embed testdata/migrations
var fixtureFS embed.FS

// Harness provides the driver-specific bits needed to run integration tests.
type Harness struct {
	// OpenDB returns a fresh, empty database connection.
	OpenDB func(t *testing.T) *sql.DB
	// Dialect returns the dialect under test.
	Dialect func(t *testing.T) migrate.Dialect
	// DumpSchema returns a deterministic string representation of the database schema.
	// The first line should be a "-- server: ..." comment with the database version.
	DumpSchema func(t *testing.T, db *sql.DB) string
	// TrackingTable is the name of the applied-migrations tracking table.
	TrackingTable string
	// SupportsTransactionalDDL indicates whether the database can roll back DDL
	// statements (CREATE TABLE, ALTER TABLE, etc.) within a transaction.
	// PostgreSQL and SQLite support this; MySQL does not.
	SupportsTransactionalDDL bool
}

// SetupFixtures copies the embedded fixture migrations to a temp directory
// and returns its path.
func SetupFixtures(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	err := fs.WalkDir(fixtureFS, "testdata/migrations", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Strip the "testdata/migrations" prefix to get the relative path.
		rel, _ := filepath.Rel("testdata/migrations", path)
		target := filepath.Join(dir, rel)

		if d.IsDir() {
			return os.MkdirAll(target, 0755)
		}

		data, err := fixtureFS.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0644)
	})
	if err != nil {
		t.Fatal(err)
	}

	return dir
}

func writeSQL(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func rowCount(t *testing.T, db *sql.DB, table string) int {
	t.Helper()
	var n int
	if err := db.QueryRow("SELECT count(*) FROM " + table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// CompareGolden compares got against the golden file at path.
// Set MMMIGRATE_UPDATE_GOLDEN=1 to overwrite the golden file.
func CompareGolden(t *testing.T, got, goldenPath string) {
	t.Helper()

	if os.Getenv("MMMIGRATE_UPDATE_GOLDEN") != "" {
		if err := os.MkdirAll(filepath.Dir(goldenPath), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(goldenPath, []byte(got), 0644); err != nil {
			t.Fatal(err)
		}
		t.Logf("updated golden file: %s", goldenPath)
		return
	}

	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden file %s: %v\n(run with MMMIGRATE_UPDATE_GOLDEN=1 to create)", goldenPath, err)
	}

	// Strip the "-- server:" line for comparison — it's informational metadata
	// that differs between environments. It's kept in the file for humans.
	gotCmp := stripServerLine(got)
	wantCmp := stripServerLine(string(want))

	if gotCmp != wantCmp {
		t.Errorf("schema does not match golden file %s\n(run with MMMIGRATE_UPDATE_GOLDEN=1 to update)\n\n%s",
			goldenPath, lineDiff(wantCmp, gotCmp))
	}
}

// stripServerLine removes the "-- server: ..." header line for comparison.
func stripServerLine(s string) string {
	if strings.HasPrefix(s, "-- server: ") {
		if i := strings.Index(s, "\n"); i >= 0 {
			return strings.TrimLeft(s[i+1:], "\n")
		}
	}
	return s
}

// lineDiff produces a unified-style diff showing only changed lines.
func lineDiff(want, got string) string {
	wantLines := strings.Split(want, "\n")
	gotLines := strings.Split(got, "\n")

	var b strings.Builder

	max := len(wantLines)
	if len(gotLines) > max {
		max = len(gotLines)
	}

	for i := 0; i < max; i++ {
		var w, g string
		if i < len(wantLines) {
			w = wantLines[i]
		}
		if i < len(gotLines) {
			g = gotLines[i]
		}
		if w == g {
			continue
		}
		if i < len(wantLines) {
			fmt.Fprintf(&b, "  line %d:\n", i+1)
		} else {
			fmt.Fprintf(&b, "  line %d (new):\n", i+1)
		}
		if w != "" || i < len(wantLines) {
			fmt.Fprintf(&b, "    - %s\n", w)
		}
		if g != "" || i < len(gotLines) {
			fmt.Fprintf(&b, "    + %s\n", g)
		}
	}

	if b.Len() == 0 {
		return "(no visible differences — possible trailing whitespace/newline mismatch)"
	}
	return b.String()
}

// Run runs the full integration test suite against the given harness.
func Run(t *testing.T, h Harness) {
	t.Run("Schema", func(t *testing.T) { testSchema(t, h) })
	t.Run("SchemaWithCurrent", func(t *testing.T) { testSchemaWithCurrent(t, h) })
	t.Run("SkipsAlreadyApplied", func(t *testing.T) { testSkipsApplied(t, h) })
	t.Run("FailedMigrationRollback", func(t *testing.T) { testFailedMigration(t, h) })
	t.Run("TestCurrentRollback", func(t *testing.T) { testTestCurrent(t, h) })
	t.Run("TestCurrentRejectsInvalid", func(t *testing.T) { testTestCurrentInvalid(t, h) })
	t.Run("EmptyDir", func(t *testing.T) { testEmptyDir(t, h) })
	t.Run("ShadowVerification", func(t *testing.T) { testShadow(t, h) })
}

// testSchema runs the fixture migrations (without current.sql) and compares
// the resulting schema to a golden file.
func testSchema(t *testing.T, h Harness) {
	db := h.OpenDB(t)
	dir := SetupFixtures(t)

	// Remove current.sql so we only apply numbered migrations.
	os.Remove(filepath.Join(dir, "current.sql"))

	ctx := context.Background()
	if err := migrate.RunMigrations(ctx, db, h.Dialect(t), dir, false); err != nil {
		t.Fatal(err)
	}

	if n := rowCount(t, db, h.TrackingTable); n != 3 {
		t.Errorf("expected 3 applied migrations, got %d", n)
	}

	got := h.DumpSchema(t, db)
	CompareGolden(t, got, filepath.Join("testdata", "schema.golden.sql"))
}

// testSchemaWithCurrent runs all fixtures including current.sql (with includes)
// and compares the resulting schema to a golden file.
func testSchemaWithCurrent(t *testing.T, h Harness) {
	db := h.OpenDB(t)
	dir := SetupFixtures(t)

	ctx := context.Background()
	if err := migrate.RunMigrations(ctx, db, h.Dialect(t), dir, true); err != nil {
		t.Fatal(err)
	}

	got := h.DumpSchema(t, db)
	CompareGolden(t, got, filepath.Join("testdata", "schema_with_current.golden.sql"))
}

func testSkipsApplied(t *testing.T, h Harness) {
	db := h.OpenDB(t)
	dir := SetupFixtures(t)
	os.Remove(filepath.Join(dir, "current.sql"))

	ctx := context.Background()
	d := h.Dialect(t)

	// Run twice — second run should be a no-op.
	if err := migrate.RunMigrations(ctx, db, d, dir, false); err != nil {
		t.Fatal(err)
	}
	if err := migrate.RunMigrations(ctx, db, d, dir, false); err != nil {
		t.Fatal(err)
	}

	if n := rowCount(t, db, h.TrackingTable); n != 3 {
		t.Errorf("expected 3 applied migrations, got %d", n)
	}
}

func testFailedMigration(t *testing.T, h Harness) {
	db := h.OpenDB(t)
	dir := SetupFixtures(t)
	os.Remove(filepath.Join(dir, "current.sql"))

	// Add a bad migration after the valid ones.
	writeSQL(t, dir, "004_bad.sql", `THIS IS NOT VALID SQL;`)

	ctx := context.Background()
	err := migrate.RunMigrations(ctx, db, h.Dialect(t), dir, false)
	if err == nil {
		t.Fatal("expected error from bad migration")
	}

	// The first 3 valid migrations should still be recorded.
	if n := rowCount(t, db, h.TrackingTable); n != 3 {
		t.Errorf("expected 3 applied migrations, got %d", n)
	}
}

func testTestCurrent(t *testing.T, h Harness) {
	if !h.SupportsTransactionalDDL {
		t.Skip("database does not support transactional DDL")
	}

	db := h.OpenDB(t)
	dir := SetupFixtures(t)

	// First apply numbered migrations so the schema exists.
	ctx := context.Background()
	if err := migrate.RunMigrations(ctx, db, h.Dialect(t), dir, false); err != nil {
		t.Fatal(err)
	}

	// TestCurrentMigration should roll back — bio column should not exist.
	if err := migrate.TestCurrentMigration(ctx, db, dir); err != nil {
		t.Fatal(err)
	}

	// Verify rollback: inserting into bio should fail.
	_, err := db.Exec("INSERT INTO users (id, name, bio) VALUES (1, 'alice', 'hi')")
	if err == nil {
		t.Error("expected bio column to not exist after TestCurrentMigration (should rollback)")
	}
}

func testTestCurrentInvalid(t *testing.T, h Harness) {
	db := h.OpenDB(t)
	dir := t.TempDir()

	writeSQL(t, dir, "current.sql", `THIS IS NOT VALID SQL;`)

	ctx := context.Background()
	if err := migrate.TestCurrentMigration(ctx, db, dir); err == nil {
		t.Error("expected error for invalid SQL")
	}
}

func testShadow(t *testing.T, h Harness) {
	shadowDB := h.OpenDB(t)
	dir := SetupFixtures(t)

	ctx := context.Background()
	d := h.Dialect(t)

	if err := migrate.VerifyAgainstShadow(ctx, shadowDB, d, dir); err != nil {
		t.Fatalf("shadow verification failed: %v", err)
	}

	// Verify the shadow has the expected schema.
	got := h.DumpSchema(t, shadowDB)
	CompareGolden(t, got, filepath.Join("testdata", "schema_with_current.golden.sql"))
}

func testEmptyDir(t *testing.T, h Harness) {
	db := h.OpenDB(t)
	dir := t.TempDir()

	ctx := context.Background()
	if err := migrate.RunMigrations(ctx, db, h.Dialect(t), dir, false); err != nil {
		t.Fatalf("running migrations on empty dir should succeed: %v", err)
	}
}
