DROP INDEX IF EXISTS alerts_reconciled_at_idx;
ALTER TABLE alerts DROP COLUMN IF EXISTS reconciled_at;
