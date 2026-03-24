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
