-- Notes are what an operator leaves for whoever picks the alert up next: what
-- was checked, what was ruled out, who was called. Alert history already
-- records what happened to an alert, but every entry there is written by
-- promview about an event. A note is written by a person about a judgement, and
-- flattening the two would bury the sentence somebody typed among a hundred
-- generated ones.
CREATE TABLE alert_notes (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    alert_id bigint NOT NULL REFERENCES alerts (id) ON DELETE CASCADE,
    -- The occurrence the note was written against. An alert that resolved and
    -- fired again is a different incident, and a note about the previous one
    -- should not read as though it describes this one.
    occurrence integer NOT NULL,
    author text NOT NULL,
    body text NOT NULL,
    created_at timestamptz NOT NULL
);

-- Notes are append-only by design: no update, no delete. They are what an
-- operator relied on at the time, and a handover note that can be quietly
-- rewritten afterwards is worth less than none. Deleting the alert takes them
-- with it, which is the cascade above.
CREATE INDEX alert_notes_alert_idx ON alert_notes (alert_id, id DESC);
