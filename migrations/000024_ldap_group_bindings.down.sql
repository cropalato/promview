-- Down removes the kind, so any binding using it has to go first: leaving one
-- behind would fail the narrowed constraint and strand the rollback halfway.
DELETE FROM role_bindings WHERE subject_kind = 'ldap_group';

ALTER TABLE role_bindings DROP CONSTRAINT role_bindings_subject_kind_check;
ALTER TABLE role_bindings DROP CONSTRAINT role_bindings_check;

ALTER TABLE role_bindings
    ADD CONSTRAINT role_bindings_subject_kind_check
    CHECK (subject_kind IN ('user', 'oidc_group'));

ALTER TABLE role_bindings
    ADD CONSTRAINT role_bindings_check CHECK (
        (subject_kind = 'user' AND user_id IS NOT NULL AND subject_issuer IS NULL AND subject_group IS NULL)
        OR
        (subject_kind = 'oidc_group' AND user_id IS NULL AND subject_issuer IS NOT NULL AND subject_group IS NOT NULL)
    );

DROP INDEX role_bindings_subject_group_idx;
CREATE INDEX role_bindings_subject_group_idx ON role_bindings (subject_issuer, subject_group)
    WHERE subject_kind = 'oidc_group';
