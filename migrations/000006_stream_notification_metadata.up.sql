ALTER TABLE stream_events
    ADD COLUMN severity text,
    ADD COLUMN alert_name text,
    ADD COLUMN summary text,
    ADD COLUMN source_slug text,
    ADD COLUMN team text;

UPDATE stream_events AS event
SET severity = COALESCE(NULLIF(alert.labels->>'severity', ''), 'warning'),
    alert_name = COALESCE(NULLIF(alert.labels->>'alertname', ''), alert.fingerprint),
    summary = COALESCE(alert.annotations->>'summary', ''),
    source_slug = alert.source_slug,
    team = COALESCE(alert.labels->>'team', '')
FROM alerts AS alert
WHERE alert.id = event.alert_id;

ALTER TABLE stream_events
    ALTER COLUMN severity SET NOT NULL,
    ALTER COLUMN alert_name SET NOT NULL,
    ALTER COLUMN summary SET NOT NULL,
    ALTER COLUMN source_slug SET NOT NULL,
    ALTER COLUMN team SET NOT NULL;
