-- Reconciliation reads the source Alertmanager directly, which closes the gap
-- the TTL sweep can only guess at: a silenced alert that clears never produces
-- a resolved notification, so nothing tells promview it ended.
ALTER TABLE alert_sources ADD COLUMN alertmanager_url text NOT NULL DEFAULT '';

-- Suppressed is a flag rather than a status because a silenced alert is still
-- firing. Collapsing the two would lose exactly the distinction an operator
-- needs during a maintenance window.
ALTER TABLE alerts ADD COLUMN suppressed boolean NOT NULL DEFAULT false;
