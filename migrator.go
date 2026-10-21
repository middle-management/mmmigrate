package mmmigrate

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"io/fs"
	"log/slog"
	"sort"
	"strings"

	"github.com/middle-management/mmmigrate/source"
)

// DefaultNamespace is the namespace used when none is given.
const DefaultNamespace = "default"

// SelfNamespace is the reserved namespace under which mmmigrate records the
// migrations of its own tracking tables (see package selfmigrations).
const SelfNamespace = "_mmmigrate"

// Migrator handles database migrations.
type Migrator struct {
	db           *sql.DB
	dialect      Dialect
	fsys         fs.FS
	applyCurrent bool
	namespace    string
}

// Option configures a Migrator.
type Option func(*Migrator)

// WithNamespace scopes the migration history to ns, so several applications
// can keep independent migrations in the same database. An empty ns means
// DefaultNamespace.
func WithNamespace(ns string) Option {
	return func(m *Migrator) { m.namespace = ns }
}

// NewMigrator creates a new migrator instance. fsys is the filesystem rooted at
// the migrations directory.
func NewMigrator(db *sql.DB, dialect Dialect, fsys fs.FS, applyCurrent bool, opts ...Option) *Migrator {
	m := &Migrator{
		db:           db,
		dialect:      dialect,
		fsys:         fsys,
		applyCurrent: applyCurrent,
	}
	for _, opt := range opts {
		opt(m)
	}
	if m.namespace == "" {
		m.namespace = DefaultNamespace
	}
	return m
}

func (m *Migrator) checkNamespace() error {
	if m.namespace == SelfNamespace {
		return fmt.Errorf("namespace %q is reserved for mmmigrate's own tracking tables", SelfNamespace)
	}
	return nil
}

// prepareTrackingTables creates the applied table if needed and brings
// mmmigrate's own tracking tables up to date. It holds the SelfNamespace lock
// so runs in different namespaces don't race to apply the same change.
func (m *Migrator) prepareTrackingTables(ctx context.Context, conn *sql.Conn) error {
	self := &Migrator{
		db:        m.db,
		dialect:   m.dialect,
		fsys:      m.dialect.SelfMigrations(),
		namespace: SelfNamespace,
	}
	if err := self.acquireLock(ctx, conn); err != nil {
		return err
	}
	defer self.releaseLock(ctx, conn)

	if _, err := conn.ExecContext(ctx, m.dialect.CreateMigrationsTable()); err != nil {
		return fmt.Errorf("failed to create migrations table: %w", err)
	}

	migrations, err := source.LoadMigrations(self.fsys, false)
	if err != nil {
		return fmt.Errorf("failed to load mmmigrate's own migrations: %w", err)
	}
	if err := self.applyNumbered(ctx, conn, migrations); err != nil {
		return fmt.Errorf("failed to upgrade mmmigrate's tracking tables: %w", err)
	}
	return nil
}

func (m *Migrator) getAppliedMigrations(ctx context.Context, conn *sql.Conn) (map[int]*source.Migration, error) {
	applied := make(map[int]*source.Migration)

	rows, err := conn.QueryContext(ctx, m.dialect.SelectApplied(), m.namespace)
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

func (m *Migrator) getCurrentChecksum(ctx context.Context, conn *sql.Conn) (string, error) {
	var checksum string
	err := conn.QueryRowContext(ctx, m.dialect.SelectCurrentChecksum(), m.namespace).Scan(&checksum)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return checksum, nil
}

func (m *Migrator) runCurrentMigration(ctx context.Context, conn *sql.Conn, mig *source.Migration) error {
	if strings.TrimSpace(mig.SQL) == "" {
		return nil
	}

	processedSQL, _, err := source.ProcessIncludes(mig.SQL, m.fsys)
	if err != nil {
		return fmt.Errorf("failed to process includes in current.sql: %w", err)
	}

	checksum := fmt.Sprintf("%x", sha256.Sum256([]byte(processedSQL)))

	currentChecksum, err := m.getCurrentChecksum(ctx, conn)
	if err != nil {
		return fmt.Errorf("failed to get current checksum: %w", err)
	}

	if checksum == currentChecksum {
		slog.Debug("current.sql unchanged, skipping")
		return nil
	}

	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to start transaction for current.sql: %w", err)
	}
	defer tx.Rollback()

	if _, err = tx.ExecContext(ctx, processedSQL); err != nil {
		return formatSQLError(processedSQL, err, "failed to execute current.sql")
	}

	if _, err = tx.ExecContext(ctx, m.dialect.UpsertCurrent(), m.namespace, checksum); err != nil {
		return fmt.Errorf("failed to record current.sql checksum: %w", err)
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit current.sql: %w", err)
	}

	slog.Info("applied current.sql", "checksum", checksum[:8])
	return nil
}

func (m *Migrator) runMigration(ctx context.Context, conn *sql.Conn, mig *source.Migration) error {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to start transaction: %w", err)
	}
	defer tx.Rollback()

	if _, err = tx.ExecContext(ctx, mig.SQL); err != nil {
		return formatSQLError(mig.SQL, err, fmt.Sprintf("failed to execute migration %d (%s)", mig.Version, mig.Name))
	}

	if _, err = tx.ExecContext(ctx, m.dialect.InsertApplied(), m.namespace, mig.Version, mig.Name); err != nil {
		return fmt.Errorf("failed to record migration %d: %w", mig.Version, err)
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit migration %d: %w", mig.Version, err)
	}

	slog.Info("applied migration", "namespace", m.namespace, "version", mig.Version, "name", mig.Name)
	return nil
}

// acquireLock takes the dialect's session-scoped lock on conn. The caller must
// release it on the same conn.
func (m *Migrator) acquireLock(ctx context.Context, conn *sql.Conn) error {
	if q := m.dialect.Lock(); q != "" {
		slog.Debug("acquiring migration lock", "namespace", m.namespace)
		if _, err := conn.ExecContext(ctx, q, m.namespace); err != nil {
			return fmt.Errorf("failed to acquire migration lock: %w", err)
		}
	}
	return nil
}

func (m *Migrator) releaseLock(ctx context.Context, conn *sql.Conn) {
	q := m.dialect.Unlock()
	if q == "" {
		return
	}
	slog.Debug("releasing migration lock", "namespace", m.namespace)

	// Release even if ctx was cancelled mid-run.
	var released sql.NullBool
	err := conn.QueryRowContext(context.WithoutCancel(ctx), q, m.namespace).Scan(&released)
	if err == nil && released.Valid && released.Bool {
		return
	}

	// The lock may still be held by this session. Discard the connection
	// rather than return it to the pool, so closing it releases the lock.
	slog.Warn("failed to release migration lock, discarding connection", "namespace", m.namespace, "error", err)
	conn.Raw(func(any) error { return driver.ErrBadConn })
}

// conn returns a dedicated connection with the tracking tables ready. The
// migration locks are session-scoped, so everything that takes one runs on
// this connection rather than whichever ones the pool hands out.
func (m *Migrator) conn(ctx context.Context) (*sql.Conn, error) {
	if err := m.checkNamespace(); err != nil {
		return nil, err
	}
	conn, err := m.db.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get database connection: %w", err)
	}
	if err := m.prepareTrackingTables(ctx, conn); err != nil {
		conn.Close()
		return nil, err
	}
	return conn, nil
}

// applyNumbered runs the numbered migrations that aren't recorded as applied,
// in version order.
func (m *Migrator) applyNumbered(ctx context.Context, conn *sql.Conn, numbered []*source.Migration) error {
	applied, err := m.getAppliedMigrations(ctx, conn)
	if err != nil {
		return err
	}

	sort.Slice(numbered, func(i, j int) bool {
		return numbered[i].Version < numbered[j].Version
	})

	for _, mig := range numbered {
		if _, ok := applied[mig.Version]; ok {
			slog.Debug("migration already applied", "namespace", m.namespace, "version", mig.Version, "name", mig.Name)
			continue
		}
		if err := m.runMigration(ctx, conn, mig); err != nil {
			return err
		}
	}
	return nil
}

// Run applies all pending migrations.
func (m *Migrator) Run(ctx context.Context, migrations []*source.Migration) error {
	conn, err := m.conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()

	if err := m.acquireLock(ctx, conn); err != nil {
		return err
	}
	defer m.releaseLock(ctx, conn)

	var numbered, current []*source.Migration
	for _, mig := range migrations {
		if mig.IsCurrent {
			current = append(current, mig)
		} else {
			numbered = append(numbered, mig)
		}
	}

	if err := m.applyNumbered(ctx, conn, numbered); err != nil {
		return err
	}

	if m.applyCurrent {
		for _, mig := range current {
			if err := m.runCurrentMigration(ctx, conn, mig); err != nil {
				return err
			}
		}
	} else if len(current) > 0 {
		slog.Warn("current.sql found but skipped (applyCurrent=false)")
	}

	return nil
}

// RunMigrations loads and applies all migrations from the given filesystem.
func RunMigrations(ctx context.Context, db *sql.DB, dialect Dialect, fsys fs.FS, applyCurrent bool, opts ...Option) error {
	migrator := NewMigrator(db, dialect, fsys, applyCurrent, opts...)

	migrations, err := source.LoadMigrations(fsys, applyCurrent)
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
func Status(ctx context.Context, db *sql.DB, dialect Dialect, fsys fs.FS, opts ...Option) ([]MigrationStatus, error) {
	migrator := NewMigrator(db, dialect, fsys, false, opts...)

	conn, err := migrator.conn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	applied, err := migrator.getAppliedMigrations(ctx, conn)
	if err != nil {
		return nil, err
	}

	migrations, err := source.LoadMigrations(fsys, false)
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
func DryRun(ctx context.Context, db *sql.DB, dialect Dialect, fsys fs.FS, applyCurrent bool, opts ...Option) ([]string, error) {
	migrator := NewMigrator(db, dialect, fsys, applyCurrent, opts...)

	conn, err := migrator.conn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	applied, err := migrator.getAppliedMigrations(ctx, conn)
	if err != nil {
		return nil, err
	}

	migrations, err := source.LoadMigrations(fsys, applyCurrent)
	if err != nil {
		return nil, fmt.Errorf("failed to load migrations: %w", err)
	}

	var pending []string

	var numbered []*source.Migration
	for _, mig := range migrations {
		if mig.IsCurrent {
			if strings.TrimSpace(mig.SQL) != "" {
				processedSQL, _, err := source.ProcessIncludes(mig.SQL, fsys)
				if err != nil {
					return nil, err
				}
				checksum := fmt.Sprintf("%x", sha256.Sum256([]byte(processedSQL)))
				currentChecksum, _ := migrator.getCurrentChecksum(ctx, conn)
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
func VerifyAgainstShadow(ctx context.Context, shadowDB *sql.DB, dialect Dialect, fsys fs.FS, opts ...Option) error {
	slog.Info("resetting shadow database")
	if err := resetShadow(ctx, shadowDB, dialect); err != nil {
		return fmt.Errorf("failed to reset shadow database: %w", err)
	}

	slog.Info("replaying all migrations on shadow database")
	if err := RunMigrations(ctx, shadowDB, dialect, fsys, true, opts...); err != nil {
		return fmt.Errorf("shadow replay failed: %w", err)
	}

	return nil
}

// resetShadow drops everything in the shadow database on one connection, so
// session settings made during the reset apply to all of it.
func resetShadow(ctx context.Context, shadowDB *sql.DB, dialect Dialect) error {
	conn, err := shadowDB.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()

	if r, ok := dialect.(Resetter); ok {
		return r.Reset(ctx, conn)
	}
	_, err = conn.ExecContext(ctx, dialect.ResetSQL())
	return err
}

// TestCurrentMigration applies current.sql in a transaction and rolls it back,
// verifying the SQL is valid without making permanent changes.
func TestCurrentMigration(ctx context.Context, db *sql.DB, fsys fs.FS) error {
	content, err := fs.ReadFile(fsys, "current.sql")
	if err != nil {
		return fmt.Errorf("failed to read current.sql: %w", err)
	}

	processedSQL, _, err := source.ProcessIncludes(string(content), fsys)
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
		return formatSQLError(processedSQL, err, "migration test failed")
	}

	// Rollback is intentional — this is a dry run. The defer handles it.
	return nil
}
