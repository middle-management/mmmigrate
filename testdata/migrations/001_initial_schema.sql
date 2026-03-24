-- Initial schema for outing.social
-- This migration creates the base tables and extensions

-- Enable PostGIS extension
CREATE EXTENSION IF NOT EXISTS postgis;

-- Cursor tracking for the Jetstream consumer (cursor = unix microseconds)
CREATE TABLE IF NOT EXISTS indexer_state (
    id          INTEGER PRIMARY KEY DEFAULT 1,
    cursor      BIGINT NOT NULL DEFAULT 0,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (id = 1)
);
INSERT INTO indexer_state (cursor) VALUES (0) ON CONFLICT DO NOTHING;

-- Cached actor profiles
CREATE TABLE IF NOT EXISTS actor (
    did          TEXT PRIMARY KEY,
    handle       TEXT NOT NULL,
    display_name TEXT,
    avatar_url   TEXT,
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_actor_handle ON actor (handle);

-- Events (social.outing.event)
CREATE TABLE IF NOT EXISTS event (
    uri               TEXT PRIMARY KEY,
    cid               TEXT NOT NULL,
    author_did        TEXT NOT NULL REFERENCES actor(did),
    title             TEXT NOT NULL,
    sport             TEXT NOT NULL,
    discipline        TEXT,
    difficulty        TEXT,
    start_lat         DOUBLE PRECISION NOT NULL,
    start_lon         DOUBLE PRECISION NOT NULL,
    start_name        TEXT,
    start_geom        GEOMETRY(Point, 4326),  -- PostGIS for geo queries
    scheduled_at      TIMESTAMPTZ NOT NULL,
    est_duration_sec  INTEGER,
    est_distance_m    DOUBLE PRECISION,
    est_elevation_m   DOUBLE PRECISION,
    max_participants  INTEGER,
    pace              TEXT,
    status            TEXT NOT NULL DEFAULT 'planned',
    club_uri          TEXT,
    polyline          TEXT,
    indexed_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_event_sport        ON event (sport);
CREATE INDEX IF NOT EXISTS idx_event_status       ON event (status);
CREATE INDEX IF NOT EXISTS idx_event_scheduled    ON event (scheduled_at);
CREATE INDEX IF NOT EXISTS idx_event_club         ON event (club_uri) WHERE club_uri IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_event_geom         ON event USING GIST (start_geom);
CREATE INDEX IF NOT EXISTS idx_event_author       ON event (author_did);

-- Auto-populate PostGIS geometry on insert/update
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

-- RSVPs (social.outing.rsvp)
CREATE TABLE IF NOT EXISTS rsvp (
    uri        TEXT PRIMARY KEY,
    cid        TEXT NOT NULL,
    actor_did  TEXT NOT NULL REFERENCES actor(did),
    event_uri  TEXT NOT NULL REFERENCES event(uri) ON DELETE CASCADE,
    status     TEXT NOT NULL,
    note       TEXT,
    indexed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (actor_did, event_uri)
);
CREATE INDEX IF NOT EXISTS idx_rsvp_event ON rsvp (event_uri);

-- Activities (social.outing.activity)
CREATE TABLE IF NOT EXISTS activity (
    uri            TEXT PRIMARY KEY,
    cid            TEXT NOT NULL,
    author_did     TEXT NOT NULL REFERENCES actor(did),
    title          TEXT,
    sport          TEXT NOT NULL,
    event_uri      TEXT REFERENCES event(uri) ON DELETE SET NULL,
    duration_sec   INTEGER NOT NULL,
    distance_m     DOUBLE PRECISION,
    elevation_m    DOUBLE PRECISION,
    avg_speed_kmh  DOUBLE PRECISION,
    avg_hr         INTEGER,
    avg_power_w    INTEGER,
    polyline       TEXT,
    gear           TEXT,
    started_at     TIMESTAMPTZ NOT NULL,
    indexed_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_activity_author    ON activity (author_did);
CREATE INDEX IF NOT EXISTS idx_activity_sport     ON activity (sport);
CREATE INDEX IF NOT EXISTS idx_activity_started   ON activity (started_at DESC);
CREATE INDEX IF NOT EXISTS idx_activity_event     ON activity (event_uri) WHERE event_uri IS NOT NULL;

-- Routes (social.outing.route)
CREATE TABLE IF NOT EXISTS route (
    uri           TEXT PRIMARY KEY,
    cid           TEXT NOT NULL,
    author_did    TEXT NOT NULL REFERENCES actor(did),
    name          TEXT NOT NULL,
    description   TEXT,
    sport         TEXT NOT NULL,
    surface       TEXT[],
    distance_m    DOUBLE PRECISION NOT NULL,
    elevation_m   DOUBLE PRECISION,
    start_lat     DOUBLE PRECISION,
    start_lon     DOUBLE PRECISION,
    start_name    TEXT,
    polyline      TEXT,
    forked_from   TEXT,
    tags          TEXT[],
    created_at    TIMESTAMPTZ,
    indexed_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_route_author ON route (author_did);
CREATE INDEX IF NOT EXISTS idx_route_sport  ON route (sport);

-- Clubs (social.outing.club)
CREATE TABLE IF NOT EXISTS club (
    uri         TEXT PRIMARY KEY,
    cid         TEXT NOT NULL,
    author_did  TEXT NOT NULL REFERENCES actor(did),
    name        TEXT NOT NULL,
    description TEXT,
    region      TEXT,
    website     TEXT,
    indexed_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_club_region ON club (region);

-- Memberships (social.outing.membership)
CREATE TABLE IF NOT EXISTS membership (
    uri        TEXT PRIMARY KEY,
    cid        TEXT NOT NULL,
    actor_did  TEXT NOT NULL REFERENCES actor(did),
    club_uri   TEXT NOT NULL REFERENCES club(uri) ON DELETE CASCADE,
    role       TEXT NOT NULL DEFAULT 'member',
    indexed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (actor_did, club_uri)
);
CREATE INDEX IF NOT EXISTS idx_membership_club ON membership (club_uri);

-- Handy view: upcoming events with RSVP counts
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