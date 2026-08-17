ALTER TABLE stream_events
    DROP COLUMN previous_labels,
    DROP COLUMN labels;

DELETE FROM sessions;
ALTER TABLE sessions
    DROP COLUMN user_id,
    ADD COLUMN subject text NOT NULL DEFAULT '',
    ADD COLUMN email text NOT NULL DEFAULT '',
    ADD COLUMN display_name text NOT NULL DEFAULT '',
    ADD COLUMN roles text[] NOT NULL DEFAULT '{}';

DROP TABLE role_binding_matchers;
DROP TABLE role_bindings;
DROP TABLE auth_identity_groups;
DROP TABLE auth_identities;
DROP TABLE users;
