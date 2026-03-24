-- Migration: create users
-- Created: 2026-03-24T13:54:07Z
-- Checksum: aff3c5ded338c9c138dc16e95e3edeb7e2d8211c6f2ed9ed1d0e683315ef10d1
-- Chain: fead836ca8f98d45f6b3a474c31cf515a0f638b94fb4709021c222c11c18cfc3
--
-- IMPORTANT: Do not modify this file after commit. The checksum above
-- tracks the integrity of this migration. Any changes will be detected
-- and may cause deployment issues.
--

CREATE TABLE users (
    id INTEGER PRIMARY KEY,
    name TEXT NOT NULL,
    email TEXT
);
