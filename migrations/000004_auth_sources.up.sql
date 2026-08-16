CREATE TABLE alert_sources (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    slug text UNIQUE NOT NULL,
    name text NOT NULL,
    token_hash bytea,
    enabled boolean NOT NULL DEFAULT true,
    last_delivery_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

INSERT INTO alert_sources (slug, name, enabled)
SELECT DISTINCT source_slug, source_slug, false
FROM alerts;

ALTER TABLE alerts
    ADD CONSTRAINT alerts_source_slug_fkey
    FOREIGN KEY (source_slug) REFERENCES alert_sources (slug);

CREATE TABLE sessions (
    token_hash bytea PRIMARY KEY,
    subject text NOT NULL,
    email text NOT NULL DEFAULT '',
    display_name text NOT NULL,
    roles text[] NOT NULL,
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    last_seen_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX sessions_expires_at_idx ON sessions (expires_at);
