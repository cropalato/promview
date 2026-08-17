CREATE TABLE users (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    email text NOT NULL DEFAULT '',
    display_name text NOT NULL,
    enabled boolean NOT NULL DEFAULT true,
    last_login_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE auth_identities (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id bigint NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    issuer text NOT NULL,
    subject text NOT NULL,
    username text NOT NULL DEFAULT '',
    email text NOT NULL DEFAULT '',
    display_name text NOT NULL DEFAULT '',
    first_seen_at timestamptz NOT NULL DEFAULT now(),
    last_seen_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (issuer, subject)
);

CREATE TABLE auth_identity_groups (
    identity_id bigint NOT NULL REFERENCES auth_identities (id) ON DELETE CASCADE,
    group_name text NOT NULL,
    observed_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (identity_id, group_name)
);

CREATE TABLE role_bindings (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name text UNIQUE NOT NULL,
    subject_kind text NOT NULL CHECK (subject_kind IN ('user', 'oidc_group')),
    user_id bigint REFERENCES users (id) ON DELETE CASCADE,
    oidc_issuer text,
    oidc_group text,
    role text NOT NULL CHECK (role IN ('viewer', 'operator', 'administrator')),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK (
        (subject_kind = 'user' AND user_id IS NOT NULL AND oidc_issuer IS NULL AND oidc_group IS NULL)
        OR
        (subject_kind = 'oidc_group' AND user_id IS NULL AND oidc_issuer IS NOT NULL AND oidc_group IS NOT NULL)
    )
);

CREATE INDEX role_bindings_user_idx ON role_bindings (user_id) WHERE user_id IS NOT NULL;
CREATE INDEX role_bindings_oidc_group_idx ON role_bindings (oidc_issuer, oidc_group)
    WHERE subject_kind = 'oidc_group';

CREATE TABLE role_binding_matchers (
    role_binding_id bigint NOT NULL REFERENCES role_bindings (id) ON DELETE CASCADE,
    ordinal integer NOT NULL,
    label_name text NOT NULL,
    operator text NOT NULL CHECK (operator IN ('=', '!=', '=~', '!~')),
    value text NOT NULL,
    PRIMARY KEY (role_binding_id, ordinal)
);

DELETE FROM sessions;
ALTER TABLE sessions
    DROP COLUMN subject,
    DROP COLUMN email,
    DROP COLUMN display_name,
    DROP COLUMN roles,
    ADD COLUMN user_id bigint NOT NULL REFERENCES users (id) ON DELETE CASCADE;

ALTER TABLE stream_events
    ADD COLUMN labels jsonb,
    ADD COLUMN previous_labels jsonb;

UPDATE stream_events AS event
SET labels = alert.labels
FROM alerts AS alert
WHERE alert.id = event.alert_id;

ALTER TABLE stream_events
    ALTER COLUMN labels SET NOT NULL;
