DROP INDEX IF EXISTS alerts_open_last_seen_idx;
ALTER TABLE alerts DROP CONSTRAINT IF EXISTS alerts_close_state_check;
ALTER TABLE alerts
    DROP COLUMN IF EXISTS closed,
    DROP COLUMN IF EXISTS closed_at,
    DROP COLUMN IF EXISTS closed_by;
