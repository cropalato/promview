#!/bin/sh
set -eu

: "${PROMVIEW_TEST_DATABASE_URL:?PROMVIEW_TEST_DATABASE_URL must point to a disposable PostgreSQL database}"

psql "$PROMVIEW_TEST_DATABASE_URL" -v ON_ERROR_STOP=1 -f migrations/000001_initial.up.sql
psql "$PROMVIEW_TEST_DATABASE_URL" -v ON_ERROR_STOP=1 -f migrations/000002_stream_events.up.sql
psql "$PROMVIEW_TEST_DATABASE_URL" -v ON_ERROR_STOP=1 -f migrations/000003_alert_history.up.sql
psql "$PROMVIEW_TEST_DATABASE_URL" -v ON_ERROR_STOP=1 -f migrations/000004_auth_sources.up.sql
psql "$PROMVIEW_TEST_DATABASE_URL" -v ON_ERROR_STOP=1 -f migrations/000005_oidc_transactions.up.sql
psql "$PROMVIEW_TEST_DATABASE_URL" -v ON_ERROR_STOP=1 -f migrations/000006_stream_notification_metadata.up.sql
psql "$PROMVIEW_TEST_DATABASE_URL" -v ON_ERROR_STOP=1 -f migrations/000007_oidc_authorization.up.sql
psql "$PROMVIEW_TEST_DATABASE_URL" -v ON_ERROR_STOP=1 -f migrations/000008_alert_acknowledgements.up.sql
psql "$PROMVIEW_TEST_DATABASE_URL" -v ON_ERROR_STOP=1 -f migrations/000009_alert_expiry.up.sql
psql "$PROMVIEW_TEST_DATABASE_URL" -v ON_ERROR_STOP=1 -f migrations/000010_alert_group_index.up.sql
psql "$PROMVIEW_TEST_DATABASE_URL" -v ON_ERROR_STOP=1 -f migrations/000011_user_preferences.up.sql
psql "$PROMVIEW_TEST_DATABASE_URL" -v ON_ERROR_STOP=1 -f migrations/000012_alert_reconciliation.up.sql

psql "$PROMVIEW_TEST_DATABASE_URL" -v ON_ERROR_STOP=1 -f migrations/000012_alert_reconciliation.down.sql
psql "$PROMVIEW_TEST_DATABASE_URL" -v ON_ERROR_STOP=1 -f migrations/000011_user_preferences.down.sql
psql "$PROMVIEW_TEST_DATABASE_URL" -v ON_ERROR_STOP=1 -f migrations/000010_alert_group_index.down.sql
psql "$PROMVIEW_TEST_DATABASE_URL" -v ON_ERROR_STOP=1 -f migrations/000009_alert_expiry.down.sql
psql "$PROMVIEW_TEST_DATABASE_URL" -v ON_ERROR_STOP=1 -f migrations/000008_alert_acknowledgements.down.sql
psql "$PROMVIEW_TEST_DATABASE_URL" -v ON_ERROR_STOP=1 -f migrations/000007_oidc_authorization.down.sql
psql "$PROMVIEW_TEST_DATABASE_URL" -v ON_ERROR_STOP=1 -f migrations/000006_stream_notification_metadata.down.sql
psql "$PROMVIEW_TEST_DATABASE_URL" -v ON_ERROR_STOP=1 -f migrations/000005_oidc_transactions.down.sql
psql "$PROMVIEW_TEST_DATABASE_URL" -v ON_ERROR_STOP=1 -f migrations/000004_auth_sources.down.sql
psql "$PROMVIEW_TEST_DATABASE_URL" -v ON_ERROR_STOP=1 -f migrations/000003_alert_history.down.sql
psql "$PROMVIEW_TEST_DATABASE_URL" -v ON_ERROR_STOP=1 -f migrations/000002_stream_events.down.sql
psql "$PROMVIEW_TEST_DATABASE_URL" -v ON_ERROR_STOP=1 -f migrations/000001_initial.down.sql
psql "$PROMVIEW_TEST_DATABASE_URL" -v ON_ERROR_STOP=1 -f migrations/000001_initial.up.sql
psql "$PROMVIEW_TEST_DATABASE_URL" -v ON_ERROR_STOP=1 -f migrations/000002_stream_events.up.sql
psql "$PROMVIEW_TEST_DATABASE_URL" -v ON_ERROR_STOP=1 -f migrations/000003_alert_history.up.sql
psql "$PROMVIEW_TEST_DATABASE_URL" -v ON_ERROR_STOP=1 -f migrations/000004_auth_sources.up.sql
psql "$PROMVIEW_TEST_DATABASE_URL" -v ON_ERROR_STOP=1 -f migrations/000005_oidc_transactions.up.sql
psql "$PROMVIEW_TEST_DATABASE_URL" -v ON_ERROR_STOP=1 -f migrations/000006_stream_notification_metadata.up.sql
psql "$PROMVIEW_TEST_DATABASE_URL" -v ON_ERROR_STOP=1 -f migrations/000007_oidc_authorization.up.sql
psql "$PROMVIEW_TEST_DATABASE_URL" -v ON_ERROR_STOP=1 -f migrations/000008_alert_acknowledgements.up.sql
psql "$PROMVIEW_TEST_DATABASE_URL" -v ON_ERROR_STOP=1 -f migrations/000009_alert_expiry.up.sql
psql "$PROMVIEW_TEST_DATABASE_URL" -v ON_ERROR_STOP=1 -f migrations/000010_alert_group_index.up.sql
psql "$PROMVIEW_TEST_DATABASE_URL" -v ON_ERROR_STOP=1 -f migrations/000011_user_preferences.up.sql
psql "$PROMVIEW_TEST_DATABASE_URL" -v ON_ERROR_STOP=1 -f migrations/000012_alert_reconciliation.up.sql

# Leave the disposable database in the same ledger-backed state used in production.
psql "$PROMVIEW_TEST_DATABASE_URL" -v ON_ERROR_STOP=1 -c 'DROP SCHEMA public CASCADE; CREATE SCHEMA public;'
PROMVIEW_DATABASE_URL="$PROMVIEW_TEST_DATABASE_URL" go run ./cmd/promview migrate
