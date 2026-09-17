ALTER INDEX role_bindings_subject_group_idx RENAME TO role_bindings_oidc_group_idx;
ALTER TABLE role_bindings RENAME COLUMN subject_group TO oidc_group;
ALTER TABLE role_bindings RENAME COLUMN subject_issuer TO oidc_issuer;
