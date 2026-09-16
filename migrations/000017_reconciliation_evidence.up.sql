-- Reconciliation confirms an alert still exists on its source Alertmanager, but
-- until now it had nowhere to record that it had. Only last_seen answered "when
-- did we last have evidence for this alert", and last_seen means one specific
-- thing: when a webhook last delivered it. Alertmanager sends no notifications
-- for a silenced alert, so a silenced alert that is demonstrably still firing
-- goes hours without one and the expiry sweep retires it as stale.
--
-- Widening last_seen to cover reconciliation was the obvious fix and the wrong
-- one: it is the console's default sort key and its pagination cursor, so
-- restamping every live alert each pass would reshuffle the table under the
-- reader and move rows across cursor boundaries.
--
-- reconciled_at is that evidence kept separately. Expiry reads the later of the
-- two, so an alert the source still holds is not retired, while an alert whose
-- source stopped answering goes stale exactly as before - which is what keeps
-- expiry a working backstop rather than something reconciliation switches off.
ALTER TABLE alerts ADD COLUMN reconciled_at timestamptz;

-- Expiry scans firing alerts ordered by staleness, and now reads this column
-- alongside last_seen for every one of them.
CREATE INDEX alerts_reconciled_at_idx ON alerts (reconciled_at);
