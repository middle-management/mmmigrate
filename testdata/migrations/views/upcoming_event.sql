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
