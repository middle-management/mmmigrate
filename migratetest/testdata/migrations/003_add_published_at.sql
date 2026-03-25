-- Migration: add published_at
-- Created: 2026-03-24T13:54:07Z
-- Checksum: dba4d2076a30e727d957f482749104dd4c76792641be8034726469fc699ce470
-- Chain: 5c122f8e2c415b1bfae325b599277d2dbd7c3f2d00b6242d291cd7fac6787a04
--
-- IMPORTANT: Do not modify this file after commit. The checksum above
-- tracks the integrity of this migration. Any changes will be detected
-- and may cause deployment issues.
--

ALTER TABLE posts ADD COLUMN published_at TEXT;
