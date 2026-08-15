CREATE TABLE alerts (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    source_slug text NOT NULL,
    fingerprint text NOT NULL,
    source_status text NOT NULL CHECK (source_status IN ('firing', 'resolved')),
    labels jsonb NOT NULL,
    annotations jsonb NOT NULL,
    starts_at timestamptz NOT NULL,
    ends_at timestamptz,
    generator_url text NOT NULL DEFAULT '',
    external_url text NOT NULL DEFAULT '',
    first_seen timestamptz NOT NULL,
    last_seen timestamptz NOT NULL,
    repeat_count bigint NOT NULL DEFAULT 0,
    UNIQUE (source_slug, fingerprint)
);

CREATE INDEX alerts_labels_idx ON alerts USING gin (labels);
CREATE INDEX alerts_status_last_seen_idx ON alerts (source_status, last_seen DESC);
CREATE INDEX alerts_source_last_seen_idx ON alerts (source_slug, last_seen DESC);
CREATE INDEX alerts_severity_idx ON alerts ((COALESCE(labels->>'severity', 'warning')));
CREATE INDEX alerts_team_idx ON alerts ((labels->>'team'));
