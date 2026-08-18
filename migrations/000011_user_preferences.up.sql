-- Console layout choices follow the operator between machines rather than
-- living in one browser. Keyed by user, so they exist only where there is a
-- user: in open mode every reader is the same anonymous principal and the
-- console keeps its choices locally instead.
CREATE TABLE user_preferences (
    user_id bigint PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    preferences jsonb NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);
