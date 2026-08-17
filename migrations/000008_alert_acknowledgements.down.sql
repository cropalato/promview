ALTER TABLE alerts
    DROP CONSTRAINT alerts_acknowledgement_state_check,
    DROP COLUMN acknowledged_by,
    DROP COLUMN acknowledged_at,
    DROP COLUMN acknowledged;
