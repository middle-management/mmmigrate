-- Migration: create current
-- Created: 2026-11-24T14:16:15Z
-- Checksum: 783644a52090f305596800f74036c47ef99d6ce6d401a3cc80aa6b02a922c25c
-- Chain: 26f34e02a140c905f368693e4a8ee93812f60c58a76434db9b40b36a04118041
--
-- IMPORTANT: Do not modify this file after commit. The checksum above
-- tracks the integrity of this migration. Any changes will be detected
-- and may cause deployment issues.
--

CREATE TABLE mmmigrate_current (
	namespace   varchar NOT NULL,
	checksum    varchar NOT NULL,
	applied_at  timestamptz NOT NULL,
	PRIMARY KEY (namespace)
);
