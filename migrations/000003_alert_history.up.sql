ALTER TABLE alerts
    ADD COLUMN occurrence integer NOT NULL DEFAULT 1,
    ADD COLUMN raw_data jsonb NOT NULL DEFAULT '{}'::jsonb;

CREATE TABLE alert_history (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    alert_id bigint NOT NULL REFERENCES alerts (id) ON DELETE CASCADE,
    occurrence integer NOT NULL,
    event_type text NOT NULL,
    source_status text NOT NULL,
    actor text NOT NULL DEFAULT '',
    message text NOT NULL DEFAULT '',
    occurred_at timestamptz NOT NULL
);

CREATE INDEX alert_history_alert_idx ON alert_history (alert_id, id DESC);

INSERT INTO alert_history (alert_id, occurrence, event_type, source_status, occurred_at)
SELECT id, occurrence, 'alert.imported', source_status, last_seen
FROM alerts;
