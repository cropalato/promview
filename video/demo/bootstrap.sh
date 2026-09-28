#!/bin/sh
# Provisions the demo operator and both sources. Runs inside the application
# image after migrations, and tolerates a re-run: the account is created once,
# and `source set` replaces a source it already knows.
set -eu

if promview user list | awk 'NR > 1 { print $2 }' | grep -qx "$DEMO_USERNAME"; then
  echo "bootstrap: user $DEMO_USERNAME already exists"
else
  printf '%s' "$DEMO_PASSWORD" | promview user create \
    --username "$DEMO_USERNAME" \
    --email "$DEMO_USERNAME@example.com" \
    --display-name 'Ada Lovelace' \
    --password-stdin
fi

user_id=$(promview user list | awk -v u="$DEMO_USERNAME" 'NR > 1 && $2 == u { print $1 }')
promview access set --name demo-operators --role operator --user-id "$user_id"

promview source set --slug production --name 'Production Alertmanager' \
  --token "$PRODUCTION_TOKEN" \
  --alertmanager-url http://alertmanager-production:9093
promview source set --slug staging --name 'Staging Alertmanager' \
  --token "$STAGING_TOKEN" \
  --alertmanager-url http://alertmanager-staging:9093

echo "bootstrap: done"
