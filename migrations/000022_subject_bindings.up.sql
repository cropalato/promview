-- A role binding names a directory and a group within it. The columns were
-- named for the only directory that existed when they were added, and LDAP
-- group names are about to live in exactly the same two columns: a column
-- called oidc_group holding an Active Directory group is a permanent tax on
-- everyone who has to read the join in resolvePrincipal.
--
-- RENAME COLUMN is catalog-only - no table rewrite - and the CHECK constraints
-- and the partial index track their columns by number, so both follow the
-- rename and need no rebuild.
ALTER TABLE role_bindings RENAME COLUMN oidc_issuer TO subject_issuer;
ALTER TABLE role_bindings RENAME COLUMN oidc_group TO subject_group;
ALTER INDEX role_bindings_oidc_group_idx RENAME TO role_bindings_subject_group_idx;
