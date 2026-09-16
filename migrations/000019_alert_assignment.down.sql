DROP INDEX IF EXISTS alerts_assigned_to_idx;
ALTER TABLE alerts DROP CONSTRAINT IF EXISTS alerts_assignment_state_check;
ALTER TABLE alerts
    DROP COLUMN IF EXISTS assigned_to,
    DROP COLUMN IF EXISTS assigned_at,
    DROP COLUMN IF EXISTS assigned_by;
