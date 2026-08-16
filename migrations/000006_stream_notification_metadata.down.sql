ALTER TABLE stream_events
    DROP COLUMN team,
    DROP COLUMN source_slug,
    DROP COLUMN summary,
    DROP COLUMN alert_name,
    DROP COLUMN severity;
