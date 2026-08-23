-- Signing in from the desktop client.
--
-- The browser flow ends by setting a cookie, which a desktop client cannot
-- receive. Instead it starts the same flow with a loopback redirect, and the
-- callback hands back a one-time code there rather than a cookie. The code is
-- then exchanged for a session over POST, so the credential never travels in a
-- URL that lands in browser history or a proxy log.
ALTER TABLE oidc_login_transactions ADD COLUMN desktop_redirect text NOT NULL DEFAULT '';

-- Only the hash is stored, for the same reason session tokens are: a database
-- copy must not be replayable. The row is deleted on first use, so a code that
-- leaks after the desktop has redeemed it is worthless.
CREATE TABLE desktop_auth_codes (
    code_hash bytea PRIMARY KEY,
    user_id bigint NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX desktop_auth_codes_expires_at_idx ON desktop_auth_codes (expires_at);
