-- Migration: create current
-- Created: 2026-10-20T18:02:46Z
-- Checksum: 86249d89ae7500764acd5b3b177d367dbd3dff8660d13ffa929363e2cfd333f2
-- Chain: 25830be61f5091e24f3d976b4414fa85a89822e9a17d7e5d05e944f3a0d4f1ca
--
-- IMPORTANT: Do not modify this file after commit. The checksum above
-- tracks the integrity of this migration. Any changes will be detected
-- and may cause deployment issues.
--

CREATE TABLE mmmigrate_current (
	namespace   VARCHAR(255) PRIMARY KEY,
	checksum    VARCHAR(64) NOT NULL,
	applied_at  TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
