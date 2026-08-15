CREATE TABLE stream_events (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    event_type text NOT NULL,
    alert_id bigint NOT NULL REFERENCES alerts (id) ON DELETE CASCADE,
    occurred_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX stream_events_occurred_at_idx ON stream_events (occurred_at);
