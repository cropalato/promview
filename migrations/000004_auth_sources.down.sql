DROP TABLE sessions;

ALTER TABLE alerts DROP CONSTRAINT alerts_source_slug_fkey;

DROP TABLE alert_sources;
