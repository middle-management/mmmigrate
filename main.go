package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/middle-management/mmmigrate/migrate"
)

func main() {
	var (
		commit        = flag.Bool("commit", false, "Commit current.sql to a numbered migration")
		description   = flag.String("description", "", "Description for the committed migration")
		check         = flag.Bool("check", false, "Check if current.sql has uncommitted changes")
		validate      = flag.Bool("validate", false, "Validate integrity of all migration files")
		applyCurrent  = flag.Bool("apply-current", false, "Apply current.sql migration (development mode)")
		run           = flag.Bool("run", false, "Run all migrations against database")
		databaseURL   = flag.String("database-url", "", "Database connection URL (defaults to DATABASE_URL env var)")
		migrationsDir = flag.String("migrations", "migrations", "Path to migrations directory")
	)
	flag.Parse()

	// Make migrations path absolute
	absDir, err := filepath.Abs(*migrationsDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: failed to resolve migrations directory: %v\n", err)
		os.Exit(1)
	}

	if *check {
		if err := migrate.CheckDirtyCurrent(absDir); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("✓ current.sql is clean")
		return
	}

	if *validate {
		entries, err := os.ReadDir(absDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: failed to read migrations directory: %v\n", err)
			os.Exit(1)
		}

		var hasErrors bool
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") || entry.Name() == "current.sql" {
				continue
			}

			filePath := filepath.Join(absDir, entry.Name())
			if err := migrate.ValidateMigrationIntegrity(filePath); err != nil {
				fmt.Fprintf(os.Stderr, "✗ %s: %v\n", entry.Name(), err)
				hasErrors = true
			} else {
				fmt.Printf("✓ %s: integrity verified\n", entry.Name())
			}
		}

		if hasErrors {
			os.Exit(1)
		}
		return
	}

	if *commit {
		if *description == "" {
			fmt.Fprintf(os.Stderr, "Error: -description is required when using -commit\n")
			flag.Usage()
			os.Exit(1)
		}

		// Test the migration against the database before committing
		dbURL := *databaseURL
		if dbURL == "" {
			dbURL = os.Getenv("DATABASE_URL")
		}
		if dbURL == "" {
			fmt.Fprintf(os.Stderr, "Error: DATABASE_URL is required to test the migration before committing\n")
			os.Exit(1)
		}

		ctx := context.Background()
		pool, err := pgxpool.New(ctx, dbURL)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: failed to connect to database: %v\n", err)
			os.Exit(1)
		}
		defer pool.Close()

		fmt.Println("Testing migration against database...")
		if err := migrate.TestCurrentMigration(ctx, pool, absDir); err != nil {
			fmt.Fprintf(os.Stderr, "✗ Migration test failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("✓ Migration test passed")

		if err := migrate.CommitCurrentMigration(absDir, *description); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	if *run || *applyCurrent {
		// Get database URL
		dbURL := *databaseURL
		if dbURL == "" {
			dbURL = os.Getenv("DATABASE_URL")
		}
		if dbURL == "" {
			fmt.Fprintf(os.Stderr, "Error: DATABASE_URL environment variable or -database-url flag required\n")
			os.Exit(1)
		}

		// Connect to database
		ctx := context.Background()
		pool, err := pgxpool.New(ctx, dbURL)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: failed to connect to database: %v\n", err)
			os.Exit(1)
		}
		defer pool.Close()

		// Run migrations
		if err := migrate.RunMigrations(ctx, pool, absDir, *applyCurrent); err != nil {
			fmt.Fprintf(os.Stderr, "Error: failed to run migrations: %v\n", err)
			os.Exit(1)
		}

		fmt.Println("✓ Migrations completed successfully")
		return
	}

	// Default: show usage
	flag.Usage()
}
