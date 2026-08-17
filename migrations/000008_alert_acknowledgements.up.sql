ALTER TABLE alerts
    ADD COLUMN acknowledged boolean NOT NULL DEFAULT false,
    ADD COLUMN acknowledged_at timestamptz,
    ADD COLUMN acknowledged_by text NOT NULL DEFAULT '';

ALTER TABLE alerts
    ADD CONSTRAINT alerts_acknowledgement_state_check CHECK (
        (acknowledged AND acknowledged_at IS NOT NULL AND acknowledged_by <> '')
        OR (NOT acknowledged AND acknowledged_at IS NULL AND acknowledged_by = '')
    );
