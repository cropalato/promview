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
