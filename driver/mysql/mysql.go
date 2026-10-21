package mysql

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"strings"

	"github.com/middle-management/mmmigrate"
	"github.com/middle-management/mmmigrate/selfmigrations"

	_ "github.com/go-sql-driver/mysql"
)

var _ mmmigrate.Dialect = Dialect{}

// Dialect implements mmmigrate.Dialect for MySQL/MariaDB.
//
// Note: MySQL DDL statements (CREATE TABLE, ALTER TABLE, etc.) are not
// transactional — they cause an implicit commit. Per-migration transactions
// protect DML but cannot roll back failed DDL.
type Dialect struct{}

func (Dialect) DriverName() string { return "mysql" }

func (Dialect) CreateMigrationsTable() string {
	return `
		CREATE TABLE IF NOT EXISTS mmmigrate_applied (
			namespace   VARCHAR(255) NOT NULL,
			version     INT NOT NULL,
			name        VARCHAR(255) NOT NULL,
			applied_at  TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (namespace, version)
		)`
}

func (Dialect) SelfMigrations() fs.FS { return selfmigrations.MySQL }

func (Dialect) SelectApplied() string {
	return "SELECT version, name, applied_at FROM mmmigrate_applied WHERE namespace = ? ORDER BY version"
}

func (Dialect) SelectCurrentChecksum() string {
	return "SELECT checksum FROM mmmigrate_current WHERE namespace = ?"
}

func (Dialect) InsertApplied() string {
	return "INSERT INTO mmmigrate_applied (namespace, version, name) VALUES (?, ?, ?)"
}

func (Dialect) UpsertCurrent() string {
	return `
		INSERT INTO mmmigrate_current (namespace, checksum) VALUES (?, ?)
		ON DUPLICATE KEY UPDATE
			checksum = VALUES(checksum),
			applied_at = CURRENT_TIMESTAMP`
}

// The lock name is per namespace. Hashing keeps it within MySQL's 64-character
// limit on lock names regardless of the namespace's length. SHA1() was removed
// in MySQL 9, so this uses a truncated SHA2().
func (Dialect) Lock() string {
	return "SELECT GET_LOCK(CONCAT('mmmigrate:', LEFT(SHA2(?, 256), 40)), -1)"
}

func (Dialect) Unlock() string {
	return "SELECT RELEASE_LOCK(CONCAT('mmmigrate:', LEFT(SHA2(?, 256), 40)))"
}

// ResetSQL is unused: MySQL can't drop stored routines or events from dynamic
// SQL, so shadow verification calls Reset instead.
func (Dialect) ResetSQL() string { return "" }

var _ mmmigrate.Resetter = Dialect{}

// Reset drops every view, table, stored procedure, stored function and event
// in the current database. Triggers go with their tables.
func (Dialect) Reset(ctx context.Context, conn *sql.Conn) error {
	rows, err := conn.QueryContext(ctx, `
		SELECT IF(table_type = 'VIEW', 'VIEW', 'TABLE') AS kind, table_name AS name
			FROM information_schema.tables WHERE table_schema = DATABASE()
		UNION ALL
		SELECT routine_type, routine_name
			FROM information_schema.routines WHERE routine_schema = DATABASE()
		UNION ALL
		SELECT 'EVENT', event_name
			FROM information_schema.events WHERE event_schema = DATABASE()
		ORDER BY FIELD(kind, 'VIEW', 'TABLE', 'PROCEDURE', 'FUNCTION', 'EVENT')`)
	if err != nil {
		return fmt.Errorf("list objects: %w", err)
	}
	var drops []string
	for rows.Next() {
		var kind, name string
		if err := rows.Scan(&kind, &name); err != nil {
			rows.Close()
			return err
		}
		drops = append(drops, "DROP "+kind+" IF EXISTS `"+strings.ReplaceAll(name, "`", "``")+"`")
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	if _, err := conn.ExecContext(ctx, "SET FOREIGN_KEY_CHECKS = 0"); err != nil {
		return err
	}
	defer conn.ExecContext(context.WithoutCancel(ctx), "SET FOREIGN_KEY_CHECKS = 1")

	for _, q := range drops {
		if _, err := conn.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("%s: %w", q, err)
		}
	}
	return nil
}
