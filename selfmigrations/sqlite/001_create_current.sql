-- Migration: create current
-- Created: 2026-10-20T18:02:46Z
-- Checksum: 4da2785e9309bc3b024c49d3a38b6b31dea21e11bb265349deb8b47151d97bba
-- Chain: 94ac405a4a2eecdc786ce65e670da6fbe2de93432395634e1bb28289f6a9a194
--
-- IMPORTANT: Do not modify this file after commit. The checksum above
-- tracks the integrity of this migration. Any changes will be detected
-- and may cause deployment issues.
--

CREATE TABLE mmmigrate_current (
	namespace   TEXT PRIMARY KEY,
	checksum    TEXT NOT NULL,
	applied_at  TEXT NOT NULL DEFAULT (datetime('now'))
);
