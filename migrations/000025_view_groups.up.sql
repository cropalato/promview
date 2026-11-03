-- View groups narrow what a user sees by default. They are a convenience and
-- never a permission: scope (role_bindings) stays the only security boundary,
-- and a view is always intersected with it.
CREATE TABLE view_groups (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name text UNIQUE NOT NULL,
    description text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- Selectors are stored the way role binding selectors are, so one grammar is
-- validated and evaluated in both places.
CREATE TABLE view_group_matchers (
    view_group_id bigint NOT NULL REFERENCES view_groups (id) ON DELETE CASCADE,
    ordinal integer NOT NULL,
    label_name text NOT NULL,
    operator text NOT NULL CHECK (operator IN ('=', '!=', '=~', '!~')),
    value text NOT NULL,
    PRIMARY KEY (view_group_id, ordinal)
);

-- Self-service membership only. Membership forced by an administrator lives in
-- view_group_pins and is never written here, so leaving a group can only ever
-- delete the user's own row.
CREATE TABLE view_group_members (
    view_group_id bigint NOT NULL REFERENCES view_groups (id) ON DELETE CASCADE,
    user_id bigint NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    joined_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (view_group_id, user_id)
);

CREATE INDEX view_group_members_user_idx ON view_group_members (user_id);

-- Who a group is forced on. Mirrors the subject columns and constraints of
-- role_bindings, and is evaluated per request the same way, so a directory
-- group membership change applies at the next login like authorization does.
CREATE TABLE view_group_pins (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    view_group_id bigint NOT NULL REFERENCES view_groups (id) ON DELETE CASCADE,
    subject_kind text NOT NULL CHECK (subject_kind IN ('user', 'oidc_group', 'ldap_group')),
    user_id bigint REFERENCES users (id) ON DELETE CASCADE,
    subject_issuer text,
    subject_group text,
    created_at timestamptz NOT NULL DEFAULT now(),
    CHECK (
        (subject_kind = 'user' AND user_id IS NOT NULL AND subject_issuer IS NULL AND subject_group IS NULL)
        OR
        (subject_kind IN ('oidc_group', 'ldap_group') AND user_id IS NULL AND subject_issuer IS NOT NULL AND subject_group IS NOT NULL)
    )
);

CREATE UNIQUE INDEX view_group_pins_user_key ON view_group_pins (view_group_id, user_id)
    WHERE subject_kind = 'user';
CREATE UNIQUE INDEX view_group_pins_group_key ON view_group_pins (view_group_id, subject_kind, subject_issuer, subject_group)
    WHERE subject_kind IN ('oidc_group', 'ldap_group');
CREATE INDEX view_group_pins_subject_idx ON view_group_pins (subject_issuer, subject_group)
    WHERE subject_kind IN ('oidc_group', 'ldap_group');
