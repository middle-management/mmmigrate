package mysql

import (
	"github.com/middle-management/mmmigrate/migrate"

	_ "github.com/go-sql-driver/mysql"
)

var _ migrate.Dialect = Dialect{}

// Dialect implements migrate.Dialect for MySQL/MariaDB.
//
// Note: MySQL DDL statements (CREATE TABLE, ALTER TABLE, etc.) are not
// transactional — they cause an implicit commit. Per-migration transactions
// protect DML but cannot roll back failed DDL.
type Dialect struct{}

func (Dialect) DriverName() string { return "mysql" }

func (Dialect) CreateMigrationsTable() string {
	return `
		CREATE TABLE IF NOT EXISTS mmmigrate_applied (
			version     INT PRIMARY KEY,
			name        VARCHAR(255) NOT NULL,
			applied_at  TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`
}

func (Dialect) CreateCurrentTable() string {
	return `
		CREATE TABLE IF NOT EXISTS mmmigrate_current (
			id          INT PRIMARY KEY DEFAULT 1,
			checksum    VARCHAR(64) NOT NULL,
			applied_at  TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			CHECK (id = 1)
		)`
}

func (Dialect) SelectApplied() string {
	return "SELECT version, name, applied_at FROM mmmigrate_applied ORDER BY version"
}

func (Dialect) SelectCurrentChecksum() string {
	return "SELECT checksum FROM mmmigrate_current WHERE id = 1"
}

func (Dialect) InsertApplied() string {
	return "INSERT INTO mmmigrate_applied (version, name) VALUES (?, ?)"
}

func (Dialect) UpsertCurrent() string {
	return `
		INSERT INTO mmmigrate_current (id, checksum) VALUES (1, ?)
		ON DUPLICATE KEY UPDATE
			checksum = VALUES(checksum),
			applied_at = CURRENT_TIMESTAMP`
}

func (Dialect) Lock() string   { return "SELECT GET_LOCK('mmmigrate', -1)" }
func (Dialect) Unlock() string { return "SELECT RELEASE_LOCK('mmmigrate')" }

func (Dialect) ResetSQL() string {
	return `
		SET FOREIGN_KEY_CHECKS = 0;
		SET @tables = NULL;
		SELECT GROUP_CONCAT(table_name) INTO @tables
			FROM information_schema.tables
			WHERE table_schema = DATABASE();
		SET @stmt = IF(@tables IS NOT NULL,
			CONCAT('DROP TABLE IF EXISTS ', @tables),
			'SELECT 1');
		PREPARE drop_stmt FROM @stmt;
		EXECUTE drop_stmt;
		DEALLOCATE PREPARE drop_stmt;
		SET FOREIGN_KEY_CHECKS = 1`
}
