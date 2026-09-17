-- Local accounts: a username and a password held here rather than in a
-- directory, for a deployment with no identity provider to point at.
--
-- A side table rather than columns on `users`, because `users` is shared with
-- OIDC and LDAP identities for whom a password column would be permanently
-- NULL. A nullable password column is the classic shape of the bug where an
-- empty password authenticates, since every read has to remember the check.
-- Here "this user has no local password" is a missing row, which the query
-- language enforces without anyone remembering anything.
CREATE TABLE local_credentials (
    user_id bigint PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    password_hash text NOT NULL,
    password_changed_at timestamptz NOT NULL DEFAULT now(),
    -- Backoff state. Kept here rather than in the process so it survives a
    -- restart and holds across replicas, which the in-process limiter does not.
    failed_attempts integer NOT NULL DEFAULT 0 CHECK (failed_attempts >= 0),
    locked_until timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- The login handle lives on the identity, not here. auth_identities already
-- carries a username column, and a second copy would be a second place that can
-- disagree about who somebody is.
--
-- Case-insensitive, so `Operator` and `operator` cannot become two accounts.
-- Partial, because only local accounts have a handle anybody types: an OIDC
-- username is whatever the provider last reported and is not unique.
CREATE UNIQUE INDEX auth_identities_local_username_idx
    ON auth_identities (lower(username))
    WHERE issuer = 'local';
