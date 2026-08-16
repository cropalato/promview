CREATE TABLE oidc_login_transactions (
    state_hash bytea PRIMARY KEY,
    nonce text NOT NULL,
    code_verifier text NOT NULL,
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX oidc_login_transactions_expires_at_idx
    ON oidc_login_transactions (expires_at);
