UPDATE alerts SET source_status = 'resolved' WHERE source_status = 'expired';

ALTER TABLE alerts DROP CONSTRAINT alerts_source_status_check;

ALTER TABLE alerts
    ADD CONSTRAINT alerts_source_status_check
    CHECK (source_status IN ('firing', 'resolved'));

ALTER TABLE alert_sources DROP COLUMN stale_after;
