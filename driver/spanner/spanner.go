// Package spanner implements mmmigrate.Dialect for Cloud Spanner databases of
// the PostgreSQL dialect.
//
// It works over any database/sql driver that reaches such a database: pgx
// through PGAdapter (Spanner's PostgreSQL wire-protocol proxy), or
// github.com/googleapis/go-sql-spanner. It imports neither; the caller
// registers the driver it uses. It needs only what both offer: PostgreSQL
// syntax with $n parameters, information_schema, and the START BATCH DDL / RUN
// BATCH / ABORT BATCH statements. go-sql-spanner binds $n parameters in the
// order they appear rather than by number, so every statement here numbers
// them in order of appearance and uses each once.
//
// Spanner differs from PostgreSQL in ways the engine has to know about:
//
//   - DDL can't run in a transaction, nor share one with DML, so migrations
//     can't be applied and recorded atomically. The dialect is an
//     mmmigrate.Executor: it runs a migration's statements outside
//     transactions, consecutive DDL as one batch (one schema change), and
//     records the migration only once all have succeeded. It keeps the
//     migration's progress meanwhile, so a run that dies part-way resumes
//     where it stopped (see ApplyMigration).
//   - There are no advisory or session locks. The dialect is an
//     mmmigrate.Locker: runs in a namespace take a lease row in
//     mmmigrate_lock with a conditional write, and renew it while they run.
//   - There is no DO block or dynamic SQL to drop everything, so shadow
//     verification resets the database with Reset.
package spanner

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	mathrand "math/rand/v2"
	"strings"
	"time"

	"github.com/middle-management/mmmigrate"
	"github.com/middle-management/mmmigrate/selfmigrations"
)

var (
	_ mmmigrate.Dialect  = Dialect{}
	_ mmmigrate.Locker   = Dialect{}
	_ mmmigrate.Executor = Dialect{}
	_ mmmigrate.Resetter = Dialect{}
)

// DefaultLeaseTTL is how long a run's lock lasts without renewal, when
// Dialect.LeaseTTL is zero. A run renews it every third of that, so another
// run waits this long at most after one dies.
const DefaultLeaseTTL = time.Minute

// Dialect implements mmmigrate.Dialect for Spanner's PostgreSQL dialect. The
// zero value is ready to use, through PGAdapter with pgx.
type Dialect struct {
	// Driver is the database/sql driver name DriverName returns, which the
	// CLI opens databases with: "pgx" (the default) for PGAdapter, or
	// "spanner" for go-sql-spanner.
	Driver string
	// LeaseTTL is how long the migration lock lasts unless renewed
	// (DefaultLeaseTTL if zero).
	LeaseTTL time.Duration
}

func (d Dialect) DriverName() string {
	if d.Driver != "" {
		return d.Driver
	}
	return "pgx"
}

// CreateMigrationsTable creates the applied table and the progress table that
// ApplyMigration keeps while a migration runs: both are needed before the
// self-migrations can be applied and recorded. They live in the default
// schema, with an mmmigrate_ prefix, as on SQLite and MySQL: Spanner's named
// schemas aren't PostgreSQL's.
func (Dialect) CreateMigrationsTable() string {
	return `
		CREATE TABLE IF NOT EXISTS mmmigrate_applied (
			namespace   varchar NOT NULL,
			version     bigint NOT NULL,
			name        varchar NOT NULL,
			applied_at  timestamptz NOT NULL,
			PRIMARY KEY (namespace, version)
		);
		CREATE TABLE IF NOT EXISTS mmmigrate_progress (
			namespace   varchar NOT NULL,
			version     bigint NOT NULL,
			done        bigint NOT NULL,
			prefix      varchar NOT NULL,
			updated_at  timestamptz NOT NULL,
			PRIMARY KEY (namespace, version)
		)`
}

func (Dialect) SelfMigrations() fs.FS { return selfmigrations.Spanner }

func (Dialect) SelectApplied() string {
	return "SELECT version, name, applied_at FROM mmmigrate_applied WHERE namespace = $1 ORDER BY version"
}

func (Dialect) SelectCurrentChecksum() string {
	return "SELECT checksum FROM mmmigrate_current WHERE namespace = $1"
}

func (Dialect) InsertApplied() string {
	return "INSERT INTO mmmigrate_applied (namespace, version, name, applied_at) VALUES ($1, $2, $3, CURRENT_TIMESTAMP)"
}

// UpsertCurrent names its conflict target: Spanner refuses ON CONFLICT
// without one.
func (Dialect) UpsertCurrent() string {
	return `
		INSERT INTO mmmigrate_current (namespace, checksum, applied_at)
		VALUES ($1, $2, CURRENT_TIMESTAMP)
		ON CONFLICT (namespace) DO UPDATE SET
			checksum = excluded.checksum,
			applied_at = excluded.applied_at`
}

// Lock and Unlock are unused: Spanner has no session locks, so the engine
// calls AcquireLock instead.
func (Dialect) Lock() string   { return "" }
func (Dialect) Unlock() string { return "" }

// ResetSQL is unused: the reset needs the catalog's contents to know what to
// drop, so shadow verification calls Reset instead.
func (Dialect) ResetSQL() string { return "" }

// --- Migration lock: a lease row ---

const createLockTable = `
	CREATE TABLE IF NOT EXISTS mmmigrate_lock (
		namespace   varchar NOT NULL,
		owner       varchar NOT NULL,
		expires_at  timestamptz NOT NULL,
		PRIMARY KEY (namespace)
	)`

// ErrLeaseLost is the cause of the context AcquireLock returns being
// cancelled when another run took the lease over after it expired unrenewed.
var ErrLeaseLost = errors.New("mmmigrate: migration lease lost")

func (d Dialect) ttl() time.Duration {
	if d.LeaseTTL > 0 {
		return d.LeaseTTL
	}
	return DefaultLeaseTTL
}

// expiry is the SQL for the lease's new expiry, by Spanner's clock, so the
// hosts' clocks don't matter.
func (d Dialect) expiry() string {
	ms := d.ttl().Milliseconds()
	if ms < 1 {
		ms = 1
	}
	return fmt.Sprintf("spanner.timestamptz_add(CURRENT_TIMESTAMP, '%d milliseconds')", ms)
}

// AcquireLock takes the namespace's lease in mmmigrate_lock, creating the
// table if it is missing. A run holds the lease when its row names the run as
// owner and hasn't expired; it takes it with a single conditional update,
// which Spanner applies atomically, so of several runs only one sees its
// update change the row. Others poll until the row is released or expires.
//
// While the lease is held it is renewed every third of LeaseTTL, on a
// connection of its own from db, so that a long DDL batch on conn doesn't hold
// up the renewal. If a renewal finds that another run has taken the lease, the
// returned context is cancelled (cause ErrLeaseLost) and the run stops at its
// next statement.
func (d Dialect) AcquireLock(ctx context.Context, db *sql.DB, conn *sql.Conn, namespace string) (context.Context, func(), error) {
	if err := d.Exec(ctx, conn, createLockTable); err != nil {
		return nil, nil, fmt.Errorf("create lock table: %w", err)
	}
	if _, err := conn.ExecContext(ctx,
		`INSERT INTO mmmigrate_lock (namespace, owner, expires_at) VALUES ($1, '', CURRENT_TIMESTAMP) ON CONFLICT (namespace) DO NOTHING`,
		namespace); err != nil {
		return nil, nil, fmt.Errorf("create lock row: %w", err)
	}

	owner := randomOwner()
	take := `UPDATE mmmigrate_lock SET owner = $1, expires_at = ` + d.expiry() + `
		WHERE namespace = $2 AND (owner = '' OR expires_at < CURRENT_TIMESTAMP)`
	wait := 50 * time.Millisecond
	for {
		res, err := conn.ExecContext(ctx, take, owner, namespace)
		if err == nil {
			if n, err := res.RowsAffected(); err == nil && n == 1 {
				break
			}
		} else if ctx.Err() != nil {
			return nil, nil, err
		} else {
			// An aborted transaction (two runs racing for the row) is
			// retried like a held lease.
			slog.Debug("migration lease: take failed, retrying", "namespace", namespace, "error", err)
		}
		if err := sleep(ctx, jitter(wait)); err != nil {
			return nil, nil, err
		}
		wait = min(wait*2, 2*time.Second)
	}

	lctx, cancel := context.WithCancelCause(ctx)
	stop := make(chan struct{})
	done := make(chan struct{})
	go d.renew(lctx, db, namespace, owner, stop, done, cancel)

	release := func() {
		close(stop)
		<-done
		defer cancel(nil)
		q := `UPDATE mmmigrate_lock SET owner = '', expires_at = CURRENT_TIMESTAMP WHERE namespace = $1 AND owner = $2`
		rctx, rcancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer rcancel()
		res, err := conn.ExecContext(rctx, q, namespace, owner)
		if err != nil {
			// conn may be broken; the lease is a row, so any connection
			// releases it.
			res, err = db.ExecContext(rctx, q, namespace, owner)
		}
		if err != nil {
			slog.Warn("failed to release migration lease; it expires on its own", "namespace", namespace, "ttl", d.ttl(), "error", err)
			return
		}
		if n, err := res.RowsAffected(); err == nil && n == 0 {
			slog.Warn("migration lease was taken over before release", "namespace", namespace)
		}
	}
	return lctx, release, nil
}

func (d Dialect) renew(ctx context.Context, db *sql.DB, namespace, owner string, stop <-chan struct{}, done chan<- struct{}, cancel context.CancelCauseFunc) {
	defer close(done)
	every := d.ttl() / 3
	t := time.NewTicker(every)
	defer t.Stop()
	q := `UPDATE mmmigrate_lock SET expires_at = ` + d.expiry() + ` WHERE namespace = $1 AND owner = $2`
	for {
		select {
		case <-stop:
			return
		case <-ctx.Done():
			return
		case <-t.C:
		}
		rctx, rcancel := context.WithTimeout(ctx, every)
		res, err := db.ExecContext(rctx, q, namespace, owner)
		rcancel()
		if err != nil {
			// Not known lost: the lease may still be ours. Try again at the
			// next tick.
			slog.Debug("migration lease: renewal failed", "namespace", namespace, "error", err)
			continue
		}
		if n, err := res.RowsAffected(); err == nil && n == 0 {
			slog.Error("migration lease lost: another run took it over after it expired", "namespace", namespace)
			cancel(ErrLeaseLost)
			return
		}
	}
}

func randomOwner() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// jitter is a random duration in [d/2, d].
func jitter(d time.Duration) time.Duration {
	return d/2 + time.Duration(mathrand.Int64N(int64(d/2)+1))
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// --- Running scripts ---

// Exec runs script outside a transaction: each run of consecutive DDL
// statements as one batch, every other statement on its own.
func (d Dialect) Exec(ctx context.Context, conn *sql.Conn, script string) error {
	for _, u := range plan(script) {
		if err := d.runUnit(ctx, conn, u, false); err != nil {
			return err
		}
	}
	return nil
}

// ApplyMigration runs a migration's statements in order, outside
// transactions, and records it as applied only once all have succeeded.
//
// The statements are grouped into units: each run of consecutive DDL is one
// unit, run as one batch (one schema change, rather than one per statement,
// each of which takes seconds on Spanner), and every other statement is a unit
// of its own. The migration's progress is kept in mmmigrate_progress: the
// number of units done and a hash of their text. A DML unit commits in the
// same transaction as the progress row that counts it, so it runs exactly
// once. A DDL batch can't share a transaction with anything, so it is counted
// after it completes. The last step records the migration in
// mmmigrate_applied and deletes its progress row, in one transaction.
//
// A run that fails or dies part-way leaves the progress row, and the next run
// resumes after the units it counts, provided the migration's text for them is
// unchanged (a later statement may have been fixed). The unit it resumes with
// may have been under way, or a DDL batch that failed part-way (Spanner keeps
// the statements of a batch that succeeded before one failed), or completed
// but not yet counted; so if it is DDL it is run in recovery mode, a statement
// at a time, each in its idempotent form where Spanner has one: CREATE ... IF
// NOT EXISTS, CREATE OR REPLACE VIEW, DROP ... IF EXISTS, ADD COLUMN IF NOT
// EXISTS. A statement without such a form (ALTER COLUMN, ADD CONSTRAINT, DROP
// COLUMN) runs as written and may fail if it had already taken effect: the
// error says so, and the operator either reverts it or marks the unit done
// (see the driver's documentation).
func (d Dialect) ApplyMigration(ctx context.Context, conn *sql.Conn, namespace string, version int, name, script string) error {
	units := plan(script)

	var done int
	var prefix string
	err := conn.QueryRowContext(ctx,
		`SELECT done, prefix FROM mmmigrate_progress WHERE namespace = $1 AND version = $2`,
		namespace, version).Scan(&done, &prefix)
	resuming := err == nil
	switch {
	case errors.Is(err, sql.ErrNoRows):
		if _, err := conn.ExecContext(ctx,
			`INSERT INTO mmmigrate_progress (namespace, version, done, prefix, updated_at) VALUES ($1, $2, 0, $3, CURRENT_TIMESTAMP)`,
			namespace, version, prefixHash(units[:0])); err != nil {
			return fmt.Errorf("record progress: %w", err)
		}
	case err != nil:
		return fmt.Errorf("read progress: %w", err)
	default:
		if done > len(units) || prefixHash(units[:done]) != prefix {
			return fmt.Errorf("migration %d (%s) was partly applied by an earlier run from SQL that differs in its first %d statement groups; "+
				"bring the database in line with the migration and delete its row from mmmigrate_progress (namespace %q, version %d)",
				version, name, done, namespace, version)
		}
		slog.Info("resuming partly applied migration", "namespace", namespace, "version", version, "name", name, "done", done, "of", len(units))
	}

	for i, u := range units {
		if i < done {
			if u.kind == kindSession {
				// Session settings don't survive the run that made them.
				if err := d.runUnit(ctx, conn, u, false); err != nil {
					return err
				}
			}
			continue
		}
		recover := resuming && i == done
		if u.kind == kindDML {
			if err := d.runDMLCounted(ctx, conn, u, namespace, version, units[:i+1]); err != nil {
				return err
			}
			continue
		}
		if err := d.runUnit(ctx, conn, u, recover); err != nil {
			if recover {
				return fmt.Errorf("%w (re-running a statement group an earlier run may have applied; "+
					"if the database already reflects it, bring it fully in line and set done = %d in its mmmigrate_progress row)", err, i+1)
			}
			return err
		}
		if _, err := conn.ExecContext(ctx,
			`UPDATE mmmigrate_progress SET done = $1, prefix = $2, updated_at = CURRENT_TIMESTAMP WHERE namespace = $3 AND version = $4`,
			i+1, prefixHash(units[:i+1]), namespace, version); err != nil {
			return fmt.Errorf("record progress: %w", err)
		}
	}

	return inTx(ctx, conn, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, Dialect{}.InsertApplied(), namespace, version, name); err != nil {
			return fmt.Errorf("record migration %d: %w", version, err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM mmmigrate_progress WHERE namespace = $1 AND version = $2`, namespace, version); err != nil {
			return fmt.Errorf("clear progress: %w", err)
		}
		return nil
	})
}

// ApplyCurrent runs current.sql like Exec and then records its checksum.
// current.sql is development-only and re-run whenever it changes, so it keeps
// no progress: write it to be re-runnable (IF NOT EXISTS).
func (d Dialect) ApplyCurrent(ctx context.Context, conn *sql.Conn, namespace, script, checksum string) error {
	if err := d.Exec(ctx, conn, script); err != nil {
		return err
	}
	if _, err := conn.ExecContext(ctx, d.UpsertCurrent(), namespace, checksum); err != nil {
		return fmt.Errorf("record current.sql checksum: %w", err)
	}
	return nil
}

// runDMLCounted runs a DML statement and counts it in the migration's progress
// in one transaction, so a run that dies doesn't leave it applied but
// uncounted.
func (d Dialect) runDMLCounted(ctx context.Context, conn *sql.Conn, u unit, namespace string, version int, through []unit) error {
	return inTx(ctx, conn, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, u.stmts[0]); err != nil {
			return &stmtError{u.stmts[0], err}
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE mmmigrate_progress SET done = $1, prefix = $2, updated_at = CURRENT_TIMESTAMP WHERE namespace = $3 AND version = $4`,
			len(through), prefixHash(through), namespace, version); err != nil {
			return fmt.Errorf("record progress: %w", err)
		}
		return nil
	})
}

func inTx(ctx context.Context, conn *sql.Conn, fn func(*sql.Tx) error) error {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

func (d Dialect) runUnit(ctx context.Context, conn *sql.Conn, u unit, recover bool) error {
	if u.kind != kindDDL {
		if _, err := conn.ExecContext(ctx, u.stmts[0]); err != nil {
			return &stmtError{u.stmts[0], err}
		}
		return nil
	}
	if recover {
		for _, q := range u.stmts {
			if err := d.runDDL(ctx, conn, []string{idempotent(q)}); err != nil {
				return err
			}
		}
		return nil
	}
	return d.runDDL(ctx, conn, u.stmts)
}

// runDDL runs DDL statements as one batch. Statements that create an object
// only if it doesn't exist are left out when it does: on Spanner every schema
// change takes seconds, even one that changes nothing, so the tracking tables'
// CREATE TABLE IF NOT EXISTS would otherwise slow every run.
//
// The emulator refuses a schema change while another is under way or a
// read-write transaction is open (another run's lock poll, say); real Spanner
// queues it. The batch is retried then.
func (d Dialect) runDDL(ctx context.Context, conn *sql.Conn, stmts []string) error {
	var batch []string
	for _, q := range stmts {
		exists, err := alreadyExists(ctx, conn, q)
		if err != nil {
			return err
		}
		if !exists {
			batch = append(batch, q)
		}
	}
	if len(batch) == 0 {
		return nil
	}
	wait := 100 * time.Millisecond
	deadline := time.Now().Add(2 * time.Minute)
	for {
		err := runBatch(ctx, conn, batch)
		if err == nil || !isConcurrentSchemaChange(err) || time.Now().After(deadline) {
			return err
		}
		slog.Debug("schema change refused while another is under way, retrying", "error", err)
		if err := sleep(ctx, jitter(wait)); err != nil {
			return err
		}
		wait = min(wait*2, 5*time.Second)
	}
}

func runBatch(ctx context.Context, conn *sql.Conn, batch []string) error {
	if len(batch) == 1 {
		if _, err := conn.ExecContext(ctx, batch[0]); err != nil {
			return &stmtError{batch[0], err}
		}
		return nil
	}
	if _, err := conn.ExecContext(ctx, "START BATCH DDL"); err != nil {
		return fmt.Errorf("START BATCH DDL: %w", err)
	}
	for _, q := range batch {
		if _, err := conn.ExecContext(ctx, q); err != nil {
			conn.ExecContext(context.WithoutCancel(ctx), "ABORT BATCH")
			return &stmtError{q, err}
		}
	}
	if _, err := conn.ExecContext(ctx, "RUN BATCH"); err != nil {
		// Some drivers leave the batch open after a failed run.
		conn.ExecContext(context.WithoutCancel(ctx), "ABORT BATCH")
		return &stmtError{strings.Join(batch, ";\n"), err}
	}
	return nil
}

func isConcurrentSchemaChange(err error) bool {
	return strings.Contains(err.Error(), "concurrent schema change")
}

// stmtError names the statement that failed. The engine prints the whole
// migration with the error; within a batch, this says which statement.
type stmtError struct {
	stmt string
	err  error
}

func (e *stmtError) Error() string { return fmt.Sprintf("%s: %v", firstLine(e.stmt), e.err) }
func (e *stmtError) Unwrap() error { return e.err }

func firstLine(q string) string {
	l, _, _ := strings.Cut(strings.TrimSpace(q), "\n")
	return l
}

func prefixHash(units []unit) string {
	h := sha256.New()
	for _, u := range units {
		for _, q := range u.stmts {
			h.Write([]byte(q))
			h.Write([]byte{0})
		}
		h.Write([]byte{1})
	}
	return hex.EncodeToString(h.Sum(nil))
}
