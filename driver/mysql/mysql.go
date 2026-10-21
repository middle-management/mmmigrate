package mysql

import (
	"github.com/middle-management/mmmigrate"

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

func (Dialect) CreateCurrentTable() string {
	return `
		CREATE TABLE IF NOT EXISTS mmmigrate_current (
			namespace   VARCHAR(255) PRIMARY KEY,
			checksum    VARCHAR(64) NOT NULL,
			applied_at  TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`
}

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
