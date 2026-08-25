-- Two things the console could not say before: which silence is holding an
-- alert back, and who asked for it.
--
-- `suppressed` alone conflates a silence with an inhibition, and an operator
-- reading a dimmed row deserves to know which one it is: an inhibition lifts
-- itself when the parent alert clears, a silence has an author and an expiry
-- somebody chose. Alertmanager already reports the distinction in the same
-- payload reconciliation reads, so storing it costs no extra request.
--
-- Empty array means not silenced. A suppressed alert with no silence ids is
-- therefore inhibited, which is exactly the case the console has to render
-- differently.
ALTER TABLE alerts ADD COLUMN silenced_by text[] NOT NULL DEFAULT '{}';

-- Silences promview created itself. Alertmanager expires them on its own
-- schedule and forgets the reasoning; keeping the author, comment and matchers
-- here is what lets the console explain a silenced alert months later, and what
-- lets it name the silence in the row rather than showing a bare id.
--
-- Rows are kept after the silence expires: the value is the record, not the
-- live state, and the live state is re-read from Alertmanager every pass.
CREATE TABLE alertmanager_silences (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    source_slug text NOT NULL,
    silence_id text NOT NULL,
    matchers jsonb NOT NULL,
    created_by text NOT NULL,
    comment text NOT NULL DEFAULT '',
    starts_at timestamptz NOT NULL,
    ends_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (source_slug, silence_id)
);

CREATE INDEX alertmanager_silences_created_at_idx ON alertmanager_silences (created_at DESC);
