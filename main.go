package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/middle-management/mmmigrate/migrate"
	"github.com/middle-management/mmmigrate/source"
)

const usage = `Usage: mmmigrate <command> [flags]

Commands:
  apply      Run all pending migrations
  commit     Commit current.sql to a numbered migration
  check      Check if current.sql has uncommitted changes
  validate   Validate integrity of all migration files

Flags:
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(1)
	}

	cmd := os.Args[1]
	args := os.Args[2:]

	switch cmd {
	case "apply":
		cmdApply(args)
	case "commit":
		cmdCommit(args)
	case "check":
		cmdCheck(args)
	case "validate":
		cmdValidate(args)
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", cmd)
		fmt.Fprint(os.Stderr, usage)
		os.Exit(1)
	}
}

func cmdApply(args []string) {
	fs := flag.NewFlagSet("apply", flag.ExitOnError)
	databaseURL := fs.String("database-url", "", "Database connection URL (defaults to DATABASE_URL env var)")
	migrationsDir := fs.String("migrations", "migrations", "Path to migrations directory")
	applyCurrent := fs.Bool("current", false, "Also apply current.sql (development mode)")
	fs.Parse(args)

	absDir := resolveDir(*migrationsDir)
	db, cleanup := openDB(*databaseURL)
	defer cleanup()

	ctx := context.Background()
	if err := migrate.RunMigrations(ctx, db, migrate.DefaultDialect(), absDir, *applyCurrent); err != nil {
		fatal("failed to run migrations: %v", err)
	}
	fmt.Println("✓ Migrations completed successfully")
}

func cmdCommit(args []string) {
	fs := flag.NewFlagSet("commit", flag.ExitOnError)
	databaseURL := fs.String("database-url", "", "Database connection URL (defaults to DATABASE_URL env var)")
	migrationsDir := fs.String("migrations", "migrations", "Path to migrations directory")
	description := fs.String("description", "", "Description for the committed migration (required)")
	fs.Parse(args)

	if *description == "" {
		fmt.Fprintln(os.Stderr, "Error: -description is required")
		fs.Usage()
		os.Exit(1)
	}

	absDir := resolveDir(*migrationsDir)
	db, cleanup := openDB(*databaseURL)
	defer cleanup()

	ctx := context.Background()
	fmt.Println("Testing migration against database...")
	if err := migrate.TestCurrentMigration(ctx, db, absDir); err != nil {
		fatal("migration test failed: %v", err)
	}
	fmt.Println("✓ Migration test passed")

	if err := source.CommitCurrentMigration(absDir, *description); err != nil {
		fatal("%v", err)
	}
}

func cmdCheck(args []string) {
	fs := flag.NewFlagSet("check", flag.ExitOnError)
	migrationsDir := fs.String("migrations", "migrations", "Path to migrations directory")
	fs.Parse(args)

	absDir := resolveDir(*migrationsDir)
	if err := source.CheckDirtyCurrent(absDir); err != nil {
		fatal("%v", err)
	}
	fmt.Println("✓ current.sql is clean")
}

func cmdValidate(args []string) {
	fs := flag.NewFlagSet("validate", flag.ExitOnError)
	migrationsDir := fs.String("migrations", "migrations", "Path to migrations directory")
	fs.Parse(args)

	absDir := resolveDir(*migrationsDir)
	entries, err := os.ReadDir(absDir)
	if err != nil {
		fatal("failed to read migrations directory: %v", err)
	}

	var hasErrors bool
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") || entry.Name() == "current.sql" {
			continue
		}

		filePath := filepath.Join(absDir, entry.Name())
		if err := source.ValidateMigrationIntegrity(filePath); err != nil {
			fmt.Fprintf(os.Stderr, "✗ %s: %v\n", entry.Name(), err)
			hasErrors = true
		} else {
			fmt.Printf("✓ %s: integrity verified\n", entry.Name())
		}
	}

	if hasErrors {
		os.Exit(1)
	}
}

func resolveDir(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		fatal("failed to resolve migrations directory: %v", err)
	}
	return abs
}

func openDB(databaseURL string) (*sql.DB, func()) {
	dialect := migrate.DefaultDialect()
	if dialect == nil {
		fatal("no database driver compiled in (build with -tags postgres or -tags sqlite)")
	}

	if databaseURL == "" {
		databaseURL = os.Getenv("DATABASE_URL")
	}
	if databaseURL == "" {
		fatal("DATABASE_URL environment variable or -database-url flag required")
	}

	db, err := sql.Open(dialect.DriverName(), databaseURL)
	if err != nil {
		fatal("failed to open database: %v", err)
	}

	if err := db.Ping(); err != nil {
		fatal("failed to connect to database: %v", err)
	}

	return db, func() { db.Close() }
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "Error: "+format+"\n", args...)
	os.Exit(1)
}
