-- Alerts whose source stops reporting them never reach a resolved notification:
-- Alertmanager suppresses resolved notifications for silenced alerts, and a
-- delivery outage drops them outright. Those alerts stay firing forever, so the
-- console needs a state for "the source went quiet" that is distinct from "the
-- source said it resolved".
ALTER TABLE alerts DROP CONSTRAINT alerts_source_status_check;

ALTER TABLE alerts
    ADD CONSTRAINT alerts_source_status_check
    CHECK (source_status IN ('firing', 'resolved', 'expired'));

-- How long an alert from this source may go unreported before it expires. NULL
-- inherits the server default. The value belongs to the source because it has to
-- exceed that Alertmanager's repeat_interval, and every source has its own.
ALTER TABLE alert_sources ADD COLUMN stale_after interval;
