-- Stream events accumulate forever. They exist only so a client that dropped
-- its connection can resume where it left off, which makes almost all of them
-- dead weight within minutes, yet nothing ever removed one.
--
-- Deleting them is the easy half. The hard half is that a client resuming from
-- a cursor older than what survives would silently miss everything in between:
-- the stream would reconnect, report no error, and hand back a console that is
-- quietly wrong about which alerts are firing. That is worse than the unbounded
-- growth, so deletion cannot ship without a way to detect it.
--
-- deleted_through is the highest id retention has removed. A client resuming
-- from at or above it has lost nothing; one resuming from below it has, and is
-- told to take a fresh snapshot rather than trusting the stream. It is kept in
-- a table rather than derived from min(id) because the answer has to survive
-- the table being emptied completely, which is exactly when it matters most.
CREATE TABLE stream_event_retention (
    singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    deleted_through bigint NOT NULL DEFAULT 0
);

INSERT INTO stream_event_retention (singleton) VALUES (true);
