-- current.sql: Development-only migration file
-- Add avatar and sports columns to club table, and name history tracking

-- Add avatar and sports columns to club table
ALTER TABLE club ADD COLUMN IF NOT EXISTS avatar TEXT;
ALTER TABLE club ADD COLUMN IF NOT EXISTS sports TEXT[];

-- Name history tracking
CREATE TABLE IF NOT EXISTS club_name_history (
    id BIGSERIAL PRIMARY KEY,
    club_uri TEXT NOT NULL REFERENCES club(uri) ON DELETE CASCADE,
    previous_name TEXT NOT NULL,
    new_name TEXT NOT NULL,
    changed_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_club_name_history_uri ON club_name_history(club_uri);
