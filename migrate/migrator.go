package migrate

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/middle-management/mmmigrate/source"
)

// Migrator handles database migrations.
type Migrator struct {
	db            *sql.DB
	dialect       Dialect
	migrationsDir string
	applyCurrent  bool
}

// NewMigrator creates a new migrator instance.
func NewMigrator(db *sql.DB, dialect Dialect, migrationsDir string, applyCurrent bool) *Migrator {
	return &Migrator{
		db:            db,
		dialect:       dialect,
		migrationsDir: migrationsDir,
		applyCurrent:  applyCurrent,
	}
}

func (m *Migrator) ensureMigrationsTable(ctx context.Context) error {
	if _, err := m.db.ExecContext(ctx, m.dialect.CreateMigrationsTable()); err != nil {
		return fmt.Errorf("failed to create migrations table: %w", err)
	}

	if m.applyCurrent {
		if _, err := m.db.ExecContext(ctx, m.dialect.CreateCurrentTable()); err != nil {
			return fmt.Errorf("failed to create current migrations table: %w", err)
		}
	}

	return nil
}

func (m *Migrator) getAppliedMigrations(ctx context.Context) (map[int]*source.Migration, error) {
	applied := make(map[int]*source.Migration)

	rows, err := m.db.QueryContext(ctx, m.dialect.SelectApplied())
	if err != nil {
		return nil, fmt.Errorf("failed to get applied migrations: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var mig source.Migration
		var appliedAt string
		if err := rows.Scan(&mig.Version, &mig.Name, &appliedAt); err != nil {
			return nil, fmt.Errorf("failed to scan migration: %w", err)
		}
		applied[mig.Version] = &mig
	}

	return applied, rows.Err()
}

func (m *Migrator) getCurrentChecksum(ctx context.Context) (string, error) {
	var checksum string
	err := m.db.QueryRowContext(ctx, m.dialect.SelectCurrentChecksum()).Scan(&checksum)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return checksum, nil
}

func (m *Migrator) runCurrentMigration(ctx context.Context, mig *source.Migration) error {
	if strings.TrimSpace(mig.SQL) == "" {
		return nil
	}

	processedSQL, _, err := source.ProcessIncludes(mig.SQL, m.migrationsDir)
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

	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to start transaction for current.sql: %w", err)
	}
	defer tx.Rollback()

	if _, err = tx.ExecContext(ctx, processedSQL); err != nil {
		return fmt.Errorf("failed to execute current.sql: %w", err)
	}

	if _, err = tx.ExecContext(ctx, m.dialect.UpsertCurrent(), checksum); err != nil {
		return fmt.Errorf("failed to record current.sql checksum: %w", err)
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit current.sql: %w", err)
	}

	slog.Info("applied current.sql", "checksum", checksum[:8])
	return nil
}

func (m *Migrator) runMigration(ctx context.Context, mig *source.Migration) error {
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to start transaction: %w", err)
	}
	defer tx.Rollback()

	if _, err = tx.ExecContext(ctx, mig.SQL); err != nil {
		return fmt.Errorf("failed to execute migration %d (%s): %w", mig.Version, mig.Name, err)
	}

	if _, err = tx.ExecContext(ctx, m.dialect.InsertApplied(), mig.Version, mig.Name); err != nil {
		return fmt.Errorf("failed to record migration %d: %w", mig.Version, err)
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit migration %d: %w", mig.Version, err)
	}

	slog.Info("applied migration", "version", mig.Version, "name", mig.Name)
	return nil
}

func (m *Migrator) acquireLock(ctx context.Context) error {
	if q := m.dialect.Lock(); q != "" {
		slog.Debug("acquiring migration lock")
		if _, err := m.db.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("failed to acquire migration lock: %w", err)
		}
	}
	return nil
}

func (m *Migrator) releaseLock(ctx context.Context) {
	if q := m.dialect.Unlock(); q != "" {
		slog.Debug("releasing migration lock")
		m.db.ExecContext(ctx, q)
	}
}

// Run applies all pending migrations.
func (m *Migrator) Run(ctx context.Context, migrations []*source.Migration) error {
	if err := m.acquireLock(ctx); err != nil {
		return err
	}
	defer m.releaseLock(ctx)

	if err := m.ensureMigrationsTable(ctx); err != nil {
		return err
	}

	applied, err := m.getAppliedMigrations(ctx)
	if err != nil {
		return err
	}

	var numbered, current []*source.Migration
	for _, mig := range migrations {
		if mig.IsCurrent {
			current = append(current, mig)
		} else {
			numbered = append(numbered, mig)
		}
	}

	sort.Slice(numbered, func(i, j int) bool {
		return numbered[i].Version < numbered[j].Version
	})

	for _, mig := range numbered {
		if _, ok := applied[mig.Version]; ok {
			slog.Debug("migration already applied", "version", mig.Version, "name", mig.Name)
			continue
		}
		if err := m.runMigration(ctx, mig); err != nil {
			return err
		}
	}

	if m.applyCurrent {
		for _, mig := range current {
			if err := m.runCurrentMigration(ctx, mig); err != nil {
				return err
			}
		}
	} else if len(current) > 0 {
		slog.Warn("current.sql found but skipped (applyCurrent=false)")
	}

	return nil
}

// RunMigrations loads and applies all migrations from the filesystem.
func RunMigrations(ctx context.Context, db *sql.DB, dialect Dialect, migrationsDir string, applyCurrent bool) error {
	migrator := NewMigrator(db, dialect, migrationsDir, applyCurrent)

	migrations, err := source.LoadMigrations(migrationsDir, applyCurrent)
	if err != nil {
		return fmt.Errorf("failed to load migrations: %w", err)
	}

	return migrator.Run(ctx, migrations)
}

// MigrationStatus describes the state of a single migration.
type MigrationStatus struct {
	Version int
	Name    string
	Applied bool
}

// Status returns the state of all migrations relative to the database.
func Status(ctx context.Context, db *sql.DB, dialect Dialect, migrationsDir string) ([]MigrationStatus, error) {
	migrator := NewMigrator(db, dialect, migrationsDir, false)

	if err := migrator.ensureMigrationsTable(ctx); err != nil {
		return nil, err
	}

	applied, err := migrator.getAppliedMigrations(ctx)
	if err != nil {
		return nil, err
	}

	migrations, err := source.LoadMigrations(migrationsDir, false)
	if err != nil {
		return nil, fmt.Errorf("failed to load migrations: %w", err)
	}

	sort.Slice(migrations, func(i, j int) bool {
		return migrations[i].Version < migrations[j].Version
	})

	var statuses []MigrationStatus
	for _, mig := range migrations {
		_, isApplied := applied[mig.Version]
		statuses = append(statuses, MigrationStatus{
			Version: mig.Version,
			Name:    mig.Name,
			Applied: isApplied,
		})
	}

	return statuses, nil
}

// DryRun returns the list of migrations that would be applied without executing them.
func DryRun(ctx context.Context, db *sql.DB, dialect Dialect, migrationsDir string, applyCurrent bool) ([]string, error) {
	migrator := NewMigrator(db, dialect, migrationsDir, applyCurrent)

	if err := migrator.ensureMigrationsTable(ctx); err != nil {
		return nil, err
	}

	applied, err := migrator.getAppliedMigrations(ctx)
	if err != nil {
		return nil, err
	}

	migrations, err := source.LoadMigrations(migrationsDir, applyCurrent)
	if err != nil {
		return nil, fmt.Errorf("failed to load migrations: %w", err)
	}

	var pending []string

	var numbered []*source.Migration
	for _, mig := range migrations {
		if mig.IsCurrent {
			if strings.TrimSpace(mig.SQL) != "" {
				processedSQL, _, err := source.ProcessIncludes(mig.SQL, migrationsDir)
				if err != nil {
					return nil, err
				}
				checksum := fmt.Sprintf("%x", sha256.Sum256([]byte(processedSQL)))
				currentChecksum, _ := migrator.getCurrentChecksum(ctx)
				if checksum != currentChecksum {
					pending = append(pending, "current.sql")
				}
			}
		} else {
			numbered = append(numbered, mig)
		}
	}

	sort.Slice(numbered, func(i, j int) bool {
		return numbered[i].Version < numbered[j].Version
	})

	// Prepend numbered migrations before current.sql.
	var result []string
	for _, mig := range numbered {
		if _, ok := applied[mig.Version]; !ok {
			result = append(result, fmt.Sprintf("%03d_%s.sql", mig.Version, mig.Name))
		}
	}
	result = append(result, pending...)

	return result, nil
}

// VerifyAgainstShadow resets a shadow database and replays all migrations plus
// current.sql from scratch, verifying the full chain works on a clean database.
func VerifyAgainstShadow(ctx context.Context, shadowDB *sql.DB, dialect Dialect, migrationsDir string) error {
	slog.Info("resetting shadow database")
	if _, err := shadowDB.ExecContext(ctx, dialect.ResetSQL()); err != nil {
		return fmt.Errorf("failed to reset shadow database: %w", err)
	}

	slog.Info("replaying all migrations on shadow database")
	if err := RunMigrations(ctx, shadowDB, dialect, migrationsDir, true); err != nil {
		return fmt.Errorf("shadow replay failed: %w", err)
	}

	return nil
}

// TestCurrentMigration applies current.sql in a transaction and rolls it back,
// verifying the SQL is valid without making permanent changes.
func TestCurrentMigration(ctx context.Context, db *sql.DB, migrationsDir string) error {
	content, err := os.ReadFile(filepath.Join(migrationsDir, "current.sql"))
	if err != nil {
		return fmt.Errorf("failed to read current.sql: %w", err)
	}

	processedSQL, _, err := source.ProcessIncludes(string(content), migrationsDir)
	if err != nil {
		return fmt.Errorf("failed to process includes: %w", err)
	}

	if strings.TrimSpace(processedSQL) == "" {
		return fmt.Errorf("current.sql is empty")
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to start transaction: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, processedSQL); err != nil {
		return fmt.Errorf("migration failed: %w", err)
	}

	// Rollback is intentional — this is a dry run. The defer handles it.
	return nil
}
