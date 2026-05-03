package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/middle-management/mmmigrate"
	"github.com/middle-management/mmmigrate/source"
)

// version is set via -ldflags="-X main.version=v1.0.0" at build time.
var version = "dev"

const usage = `Usage: mmmigrate <command> [flags]

Commands:
  init       Initialize a new migrations directory
  apply      Run all pending migrations
  commit     Commit current.sql to a numbered migration
  revert     Revert last committed migration back to current.sql
  status     Show migration status
  render     Render current.sql with includes expanded (to stdout)
  check      Check if current.sql has uncommitted changes
  validate   Validate checksums and chain integrity
  watch      Watch current.sql and its includes, re-apply on change
  version    Print version

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
	case "init":
		cmdInit(args)
	case "apply":
		cmdApply(args)
	case "commit":
		cmdCommit(args)
	case "revert":
		cmdRevert(args)
	case "status":
		cmdStatus(args)
	case "render":
		cmdRender(args)
	case "check":
		cmdCheck(args)
	case "validate":
		cmdValidate(args)
	case "watch":
		cmdWatch(args)
	case "version":
		fmt.Println(version)
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", cmd)
		fmt.Fprint(os.Stderr, usage)
		os.Exit(1)
	}
}

func cmdInit(args []string) {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	migrationsDir := fs.String("migrations", "migrations", "Path to migrations directory")
	fs.Parse(args)

	if err := source.Init(*migrationsDir); err != nil {
		fatal("%v", err)
	}
	fmt.Printf("✓ Initialized %s\n", *migrationsDir)
}

func cmdApply(args []string) {
	fs := flag.NewFlagSet("apply", flag.ExitOnError)
	databaseURL := fs.String("database-url", "", "Database connection URL (defaults to DATABASE_URL env var)")
	migrationsDir := fs.String("migrations", "migrations", "Path to migrations directory")
	applyCurrent := fs.Bool("current", false, "Also apply current.sql (development mode)")
	dryRun := fs.Bool("dry-run", false, "Show what would be applied without executing")
	fs.Parse(args)

	absDir := resolveDir(*migrationsDir)
	db, cleanup := openDB(*databaseURL)
	defer cleanup()

	ctx := context.Background()
	fsys := os.DirFS(absDir)

	if *dryRun {
		pending, err := mmmigrate.DryRun(ctx, db, dialect, fsys, *applyCurrent)
		if err != nil {
			fatal("%v", err)
		}
		if len(pending) == 0 {
			fmt.Println("Nothing to apply")
			return
		}
		fmt.Println("Would apply:")
		for _, name := range pending {
			fmt.Printf("  %s\n", name)
		}
		return
	}

	if err := mmmigrate.RunMigrations(ctx, db, dialect, fsys, *applyCurrent); err != nil {
		fatal("%v", err)
	}
	fmt.Println("✓ Migrations completed successfully")
}

func cmdCommit(args []string) {
	fs := flag.NewFlagSet("commit", flag.ExitOnError)
	databaseURL := fs.String("database-url", "", "Database connection URL (defaults to DATABASE_URL env var)")
	shadowURL := fs.String("shadow-url", "", "Shadow database URL for full replay verification (defaults to SHADOW_DATABASE_URL env var)")
	migrationsDir := fs.String("migrations", "migrations", "Path to migrations directory")
	description := fs.String("description", "", "Description for the committed migration (required)")
	skipVerify := fs.Bool("skip-verify", false, "Skip migration verification (commit without testing against a database)")
	fs.Parse(args)

	if *description == "" {
		fmt.Fprintln(os.Stderr, "Error: -description is required")
		fs.Usage()
		os.Exit(1)
	}

	absDir := resolveDir(*migrationsDir)

	if !*skipVerify {
		db, cleanup := openDB(*databaseURL)
		defer cleanup()

		ctx := context.Background()
		fmt.Println("Testing migration against database...")
		if err := mmmigrate.TestCurrentMigration(ctx, db, os.DirFS(absDir)); err != nil {
			fatal("migration test failed: %v", err)
		}
		fmt.Println("✓ Migration test passed")
	}

	if err := source.CommitCurrentMigration(absDir, *description); err != nil {
		fatal("%v", err)
	}

	if !*skipVerify {
		shadow := *shadowURL
		if shadow == "" {
			shadow = os.Getenv("SHADOW_DATABASE_URL")
		}
		if shadow != "" {
			shadowDB, err := sql.Open(dialect.DriverName(), shadow)
			if err != nil {
				fatal("failed to open shadow database: %v", err)
			}
			defer shadowDB.Close()

			ctx := context.Background()
			fmt.Println("Verifying full migration chain against shadow database...")
			if err := mmmigrate.VerifyAgainstShadow(ctx, shadowDB, dialect, os.DirFS(absDir)); err != nil {
				fatal("shadow verification failed: %v", err)
			}
			fmt.Println("✓ Shadow database verification passed")
		}
	}
}

func cmdRevert(args []string) {
	fs := flag.NewFlagSet("revert", flag.ExitOnError)
	migrationsDir := fs.String("migrations", "migrations", "Path to migrations directory")
	fs.Parse(args)

	absDir := resolveDir(*migrationsDir)
	if err := source.RevertLastMigration(absDir); err != nil {
		fatal("%v", err)
	}
}

func cmdStatus(args []string) {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	databaseURL := fs.String("database-url", "", "Database connection URL (defaults to DATABASE_URL env var)")
	migrationsDir := fs.String("migrations", "migrations", "Path to migrations directory")
	fs.Parse(args)

	absDir := resolveDir(*migrationsDir)
	db, cleanup := openDB(*databaseURL)
	defer cleanup()

	ctx := context.Background()
	statuses, err := mmmigrate.Status(ctx, db, dialect, os.DirFS(absDir))
	if err != nil {
		fatal("%v", err)
	}

	if len(statuses) == 0 {
		fmt.Println("No migrations found")
		return
	}

	for _, s := range statuses {
		mark := "  "
		if s.Applied {
			mark = "✓ "
		}
		fmt.Printf("%s%03d_%s\n", mark, s.Version, s.Name)
	}

	// Also show current.sql status.
	if err := source.CheckDirtyCurrent(absDir); err != nil {
		fmt.Println("\ncurrent.sql has uncommitted changes")
	}
}

func cmdRender(args []string) {
	fs := flag.NewFlagSet("render", flag.ExitOnError)
	migrationsDir := fs.String("migrations", "migrations", "Path to migrations directory")
	fs.Parse(args)

	absDir := resolveDir(*migrationsDir)
	rendered, err := source.Render(os.DirFS(absDir))
	if err != nil {
		fatal("%v", err)
	}
	fmt.Print(rendered)
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
	if err := source.ValidateChain(absDir); err != nil {
		fatal("validation failed: %v", err)
	}
	fmt.Println("✓ All migrations verified (checksums and chain integrity)")
}

func resolveDir(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		fatal("failed to resolve migrations directory: %v", err)
	}
	return abs
}

func openDB(databaseURL string) (*sql.DB, func()) {
	if dialect == nil {
		fatal("no database driver compiled in (build with -tags postgres, -tags sqlite, or -tags mysql)")
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
