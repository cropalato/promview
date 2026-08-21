-- Creating a silence is the first time promview writes to an Alertmanager
-- rather than reading it. Reads work unauthenticated in the deployments this
-- targets, but writes are the dangerous direction and are commonly protected,
-- so a source can carry a credential for them.
--
-- The token is stored as given, not hashed: unlike an ingestion token, which
-- promview only ever compares against, this one has to be replayed to the
-- Alertmanager on every request. Treat the column as a secret at rest.
ALTER TABLE alert_sources ADD COLUMN alertmanager_token text NOT NULL DEFAULT '';
