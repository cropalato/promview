-- Close is the operator saying "this is handled" about an alert promview is
-- still receiving. It is deliberately not a source_status: firing, resolved and
-- expired are all claims about what the source reports, and close is a claim
-- about what an operator decided. Folding it into the same field would make the
-- console unable to say an alert is both still firing and already dealt with,
-- which is exactly the state close exists to express.
ALTER TABLE alerts
    ADD COLUMN closed boolean NOT NULL DEFAULT false,
    ADD COLUMN closed_at timestamptz,
    ADD COLUMN closed_by text NOT NULL DEFAULT '';

ALTER TABLE alerts
    ADD CONSTRAINT alerts_close_state_check CHECK (
        (closed AND closed_at IS NOT NULL AND closed_by <> '')
        OR (NOT closed AND closed_at IS NULL AND closed_by = '')
    );

-- The default list excludes closed alerts, so this index serves the common
-- query rather than the rare one.
CREATE INDEX alerts_open_last_seen_idx ON alerts (last_seen DESC) WHERE NOT closed;
