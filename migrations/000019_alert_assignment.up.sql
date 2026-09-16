-- Assignment is the operator action that says who has picked an alert up. It
-- sits beside acknowledgement rather than inside it: acknowledging says somebody
-- has seen this, assigning says somebody owns it, and on a busy rotation those
-- are different claims made by different people at different times.
--
-- The assignee is free text rather than a reference to users. An alert is
-- routinely handed to somebody who has never signed in to promview - a vendor,
-- a team rota address, the person named in a runbook - and a foreign key would
-- turn every one of those into an error instead of an assignment.
ALTER TABLE alerts
    ADD COLUMN assigned_to text NOT NULL DEFAULT '',
    ADD COLUMN assigned_at timestamptz,
    ADD COLUMN assigned_by text NOT NULL DEFAULT '';

-- The same shape as the acknowledgement constraint: either all three fields
-- describe an assignment, or none of them do. A half-written assignment is a
-- row nobody can render honestly.
ALTER TABLE alerts
    ADD CONSTRAINT alerts_assignment_state_check CHECK (
        (assigned_to <> '' AND assigned_at IS NOT NULL AND assigned_by <> '')
        OR (assigned_to = '' AND assigned_at IS NULL AND assigned_by = '')
    );

-- "What is on my plate" is the query this exists to serve.
CREATE INDEX alerts_assigned_to_idx ON alerts (assigned_to) WHERE assigned_to <> '';
