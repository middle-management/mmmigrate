package migrate

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Migration represents a single migration.
type Migration struct {
	Version   int
	Name      string
	SQL       string
	AppliedAt *time.Time
	IsCurrent bool // true for current.sql migrations
}

// Migrator handles database migrations.
type Migrator struct {
	pool         *pgxpool.Pool
	applyCurrent bool // whether to apply current.sql
}

// NewMigrator creates a new migrator instance.
func NewMigrator(pool *pgxpool.Pool, applyCurrent bool) *Migrator {
	return &Migrator{
		pool:         pool,
		applyCurrent: applyCurrent,
	}
}

// ensureMigrationsSchema creates the migrations schema and table if they don't exist.
func (m *Migrator) ensureMigrationsSchema(ctx context.Context) error {
	_, err := m.pool.Exec(ctx, "CREATE SCHEMA IF NOT EXISTS migrations")
	if err != nil {
		return fmt.Errorf("failed to create migrations schema: %w", err)
	}

	_, err = m.pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS migrations.applied (
			version     INTEGER PRIMARY KEY,
			name        TEXT NOT NULL,
			applied_at  TIMESTAMPTZ NOT NULL DEFAULT now()
		)
	`)
	if err != nil {
		return fmt.Errorf("failed to create migrations.applied table: %w", err)
	}

	if m.applyCurrent {
		_, err = m.pool.Exec(ctx, `
			CREATE TABLE IF NOT EXISTS migrations.current (
				id          INTEGER PRIMARY KEY DEFAULT 1,
				checksum    TEXT NOT NULL,
				applied_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
				CONSTRAINT  current_single_row CHECK (id = 1)
			)
		`)
		if err != nil {
			return fmt.Errorf("failed to create migrations.current table: %w", err)
		}
	}

	return nil
}

// getAppliedMigrations returns a map of applied migration versions.
func (m *Migrator) getAppliedMigrations(ctx context.Context) (map[int]*Migration, error) {
	applied := make(map[int]*Migration)

	rows, err := m.pool.Query(ctx, "SELECT version, name, applied_at FROM migrations.applied ORDER BY version")
	if err != nil {
		return nil, fmt.Errorf("failed to get applied migrations: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var migration Migration
		if err := rows.Scan(&migration.Version, &migration.Name, &migration.AppliedAt); err != nil {
			return nil, fmt.Errorf("failed to scan migration: %w", err)
		}
		applied[migration.Version] = &migration
	}

	return applied, nil
}

// getCurrentChecksum gets the checksum of the currently applied current.sql.
func (m *Migrator) getCurrentChecksum(ctx context.Context) (string, error) {
	var checksum string
	err := m.pool.QueryRow(ctx, "SELECT checksum FROM migrations.current WHERE id = 1").Scan(&checksum)
	if err != nil {
		return "", nil
	}
	return checksum, nil
}

// runCurrentMigration applies the current.sql migration if it has changed.
func (m *Migrator) runCurrentMigration(ctx context.Context, migration *Migration) error {
	if strings.TrimSpace(migration.SQL) == "" {
		return nil
	}

	processedSQL, _, err := processIncludes(migration.SQL, "migrations")
	if err != nil {
		return fmt.Errorf("failed to process includes in current.sql: %w", err)
	}

	checksum := fmt.Sprintf("%x", sha256.Sum256([]byte(processedSQL)))

	currentChecksum, err := m.getCurrentChecksum(ctx)
	if err != nil {
		return fmt.Errorf("failed to get current checksum: %w", err)
	}

	if checksum == currentChecksum {
		slog.Debug("current.sql unchanged, skipping")
		return nil
	}

	tx, err := m.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to start transaction for current.sql: %w", err)
	}
	defer tx.Rollback(ctx)

	_, err = tx.Exec(ctx, processedSQL)
	if err != nil {
		return fmt.Errorf("failed to execute current.sql: %w", err)
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO migrations.current (id, checksum, applied_at)
		VALUES (1, $1, now())
		ON CONFLICT (id) DO UPDATE SET
			checksum = EXCLUDED.checksum,
			applied_at = EXCLUDED.applied_at
	`, checksum)
	if err != nil {
		return fmt.Errorf("failed to record current.sql checksum: %w", err)
	}

	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("failed to commit current.sql: %w", err)
	}

	slog.Info("applied current.sql", "checksum", checksum[:8])
	return nil
}

// runMigration applies a single migration within a transaction.
func (m *Migrator) runMigration(ctx context.Context, migration *Migration) error {
	tx, err := m.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to start transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	_, err = tx.Exec(ctx, migration.SQL)
	if err != nil {
		return fmt.Errorf("failed to execute migration %d (%s): %w", migration.Version, migration.Name, err)
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO migrations.applied (version, name, applied_at)
		VALUES ($1, $2, now())
	`, migration.Version, migration.Name)
	if err != nil {
		return fmt.Errorf("failed to record migration %d: %w", migration.Version, err)
	}

	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("failed to commit migration %d: %w", migration.Version, err)
	}

	slog.Info("applied migration", "version", migration.Version, "name", migration.Name)
	return nil
}

// Run applies all pending migrations.
func (m *Migrator) Run(ctx context.Context, migrations []*Migration) error {
	if err := m.ensureMigrationsSchema(ctx); err != nil {
		return err
	}

	applied, err := m.getAppliedMigrations(ctx)
	if err != nil {
		return err
	}

	var numberedMigrations []*Migration
	var currentMigrations []*Migration

	for _, migration := range migrations {
		if migration.IsCurrent {
			currentMigrations = append(currentMigrations, migration)
		} else {
			numberedMigrations = append(numberedMigrations, migration)
		}
	}

	sort.Slice(numberedMigrations, func(i, j int) bool {
		return numberedMigrations[i].Version < numberedMigrations[j].Version
	})

	for _, migration := range numberedMigrations {
		if _, isApplied := applied[migration.Version]; isApplied {
			slog.Debug("migration already applied", "version", migration.Version, "name", migration.Name)
			continue
		}

		if err := m.runMigration(ctx, migration); err != nil {
			return err
		}
	}

	if m.applyCurrent {
		for _, migration := range currentMigrations {
			if err := m.runCurrentMigration(ctx, migration); err != nil {
				return err
			}
		}
	} else {
		if len(currentMigrations) > 0 {
			slog.Warn("current.sql found but skipped (applyCurrent=false)")
		}
	}

	return nil
}

// RunMigrations applies all migrations from the filesystem.
func RunMigrations(ctx context.Context, pool *pgxpool.Pool, migrationsDir string, applyCurrent bool) error {
	migrator := NewMigrator(pool, applyCurrent)

	migrations, err := LoadMigrations(migrationsDir, applyCurrent)
	if err != nil {
		return fmt.Errorf("failed to load filesystem migrations: %w", err)
	}

	return migrator.Run(ctx, migrations)
}

// TestCurrentMigration applies current.sql in a transaction and rolls it back,
// verifying the SQL is valid without making permanent changes.
func TestCurrentMigration(ctx context.Context, pool *pgxpool.Pool, migrationsDir string) error {
	content, err := os.ReadFile(filepath.Join(migrationsDir, "current.sql"))
	if err != nil {
		return fmt.Errorf("failed to read current.sql: %w", err)
	}

	processedSQL, _, err := processIncludes(string(content), migrationsDir)
	if err != nil {
		return fmt.Errorf("failed to process includes: %w", err)
	}

	if strings.TrimSpace(processedSQL) == "" {
		return fmt.Errorf("current.sql is empty")
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to start transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, processedSQL); err != nil {
		return fmt.Errorf("migration failed: %w", err)
	}

	return tx.Rollback(ctx)
}
