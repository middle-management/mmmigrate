-- Migration: create posts
-- Created: 2026-03-24T13:54:07Z
-- Checksum: d6efa06c141115252e7fb4e2ba53a464573d7c8f706d3efc2172804414d3a740
-- Chain: 7a774ce86df24ff2a139a6dbbe72fb9f3582b91eb7e699972978c752326da865
--
-- IMPORTANT: Do not modify this file after commit. The checksum above
-- tracks the integrity of this migration. Any changes will be detected
-- and may cause deployment issues.
--

CREATE TABLE posts (
    id INTEGER PRIMARY KEY,
    user_id INTEGER NOT NULL,
    title TEXT NOT NULL,
    body TEXT
);
