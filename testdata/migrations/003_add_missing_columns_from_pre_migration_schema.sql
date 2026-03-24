-- Migration: add missing columns from pre-migration schema
-- Created: 2026-03-22T15:43:13Z
-- Checksum: 449a3e5043fdc0c401bb706c8595e0b19865d6fc21333248212ea8171fa8784b
--
-- IMPORTANT: Do not modify this file after commit. The checksum above
-- tracks the integrity of this migration. Any changes will be detected
-- and may cause deployment issues.
--
-- This migration was compiled from current.sql with includes:
--   - functions/event/set_geom.sql (lines 27-40) [ddcb188b]
--   - views/upcoming_event.sql (lines 41-61) [26dc1fce]
--

-- current.sql: Development-only migration file
--
-- This file is automatically applied in development environments.
-- When ready for production, commit this as a numbered migration file (NNN_name.sql)
-- and clear this file.

-- Add columns missing from production route table (pre-migration schema was older)
ALTER TABLE route ADD COLUMN IF NOT EXISTS description  TEXT;
ALTER TABLE route ADD COLUMN IF NOT EXISTS surface      TEXT[];
ALTER TABLE route ADD COLUMN IF NOT EXISTS start_lat    DOUBLE PRECISION;
ALTER TABLE route ADD COLUMN IF NOT EXISTS start_lon    DOUBLE PRECISION;
ALTER TABLE route ADD COLUMN IF NOT EXISTS start_name   TEXT;
ALTER TABLE route ADD COLUMN IF NOT EXISTS tags         TEXT[];
ALTER TABLE route ADD COLUMN IF NOT EXISTS created_at   TIMESTAMPTZ;

-- Add missing unique constraints (IF NOT EXISTS not supported for constraints, use DO block)
DO $$ BEGIN
  ALTER TABLE rsvp ADD CONSTRAINT rsvp_actor_event_unique UNIQUE (actor_did, event_uri);
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

DO $$ BEGIN
  ALTER TABLE membership ADD CONSTRAINT membership_actor_club_unique UNIQUE (actor_did, club_uri);
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

-- BEGIN INCLUDE: functions/event/set_geom.sql [checksum: ddcb188b]
CREATE OR REPLACE FUNCTION event_set_geom() RETURNS TRIGGER AS $$
BEGIN
    NEW.start_geom := ST_SetSRID(ST_MakePoint(NEW.start_lon, NEW.start_lat), 4326);
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_event_geom ON event;
CREATE TRIGGER trg_event_geom
    BEFORE INSERT OR UPDATE OF start_lat, start_lon ON event
    FOR EACH ROW EXECUTE FUNCTION event_set_geom();

-- END INCLUDE: functions/event/set_geom.sql
-- BEGIN INCLUDE: views/upcoming_event.sql [checksum: 26dc1fce]
CREATE OR REPLACE VIEW upcoming_event AS
SELECT
    e.*,
    a.handle AS author_handle,
    a.display_name AS author_display_name,
    COALESCE(r.going, 0) AS rsvp_going,
    COALESCE(r.maybe, 0) AS rsvp_maybe
FROM event e
JOIN actor a ON a.did = e.author_did
LEFT JOIN LATERAL (
    SELECT
        COUNT(*) FILTER (WHERE status = 'going') AS going,
        COUNT(*) FILTER (WHERE status = 'maybe') AS maybe
    FROM rsvp WHERE event_uri = e.uri
) r ON true
WHERE e.status = 'planned'
  AND e.scheduled_at > now()
ORDER BY e.scheduled_at ASC;

-- END INCLUDE: views/upcoming_event.sql
