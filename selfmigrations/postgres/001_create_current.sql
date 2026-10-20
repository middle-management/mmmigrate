-- Migration: create current
-- Created: 2026-10-20T18:02:46Z
-- Checksum: a2b76cab8302b06f0a9b56aef59e069e6ba226c3f4e872e873701a1a2f1ea4eb
-- Chain: 41e99827f97f99b1a0a44c9368849feeb31911ea28888dbb809e46dc07e6ca05
--
-- IMPORTANT: Do not modify this file after commit. The checksum above
-- tracks the integrity of this migration. Any changes will be detected
-- and may cause deployment issues.
--

CREATE TABLE mmmigrate.current (
	namespace   TEXT PRIMARY KEY,
	checksum    TEXT NOT NULL,
	applied_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
