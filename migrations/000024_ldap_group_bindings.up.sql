-- LDAP groups bind through the same two columns OIDC groups already use: the
-- columns name a directory and a group within it, which is what both are.
-- Only the subject kind is new.
--
-- Both CHECK constraints have to be rebuilt because both name the permitted
-- kinds. The compound one is otherwise unchanged: a group binding still carries
-- an issuer and a group and no user, whichever directory it names.
ALTER TABLE role_bindings DROP CONSTRAINT role_bindings_subject_kind_check;
ALTER TABLE role_bindings DROP CONSTRAINT role_bindings_check;

ALTER TABLE role_bindings
    ADD CONSTRAINT role_bindings_subject_kind_check
    CHECK (subject_kind IN ('user', 'oidc_group', 'ldap_group'));

ALTER TABLE role_bindings
    ADD CONSTRAINT role_bindings_check CHECK (
        (subject_kind = 'user' AND user_id IS NOT NULL AND subject_issuer IS NULL AND subject_group IS NULL)
        OR
        (subject_kind IN ('oidc_group', 'ldap_group') AND user_id IS NULL AND subject_issuer IS NOT NULL AND subject_group IS NOT NULL)
    );

-- The partial index was restricted to OIDC group bindings, so an LDAP one would
-- not have been indexed at all.
DROP INDEX role_bindings_subject_group_idx;
CREATE INDEX role_bindings_subject_group_idx ON role_bindings (subject_issuer, subject_group)
    WHERE subject_kind IN ('oidc_group', 'ldap_group');
