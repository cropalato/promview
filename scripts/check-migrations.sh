#!/bin/sh
set -eu

: "${PROMVIEW_TEST_DATABASE_URL:?PROMVIEW_TEST_DATABASE_URL must point to a disposable PostgreSQL database}"

# Enumerated from the directory rather than listed here. The list used to be
# written out by hand and stopped being updated at 000014, so eight migrations -
# and every down migration among them - were skipped by the check whose whole
# job is to run them. A list that has to be edited to stay correct is a list
# that silently stops being correct.
apply() {
	psql "$PROMVIEW_TEST_DATABASE_URL" -v ON_ERROR_STOP=1 -q -f "$1"
}

ups=""
downs=""
for up in migrations/*.up.sql; do
	down="${up%.up.sql}.down.sql"
	# A migration with no down cannot be rolled back, and an operator finds
	# that out during the rollback rather than here.
	if [ ! -f "$down" ]; then
		echo "check-migrations: $up has no matching $down" >&2
		exit 1
	fi
	ups="$ups $up"
	downs="$down $downs"
done

for migration in $ups; do apply "$migration"; done
# Down in reverse order, then up again: a down migration that leaves the schema
# subtly different fails the second pass rather than a future deployment.
for migration in $downs; do apply "$migration"; done
for migration in $ups; do apply "$migration"; done

count=$(echo "$ups" | wc -w)
echo "check-migrations: $count migrations applied, rolled back, and reapplied"

# Leave the disposable database in the same ledger-backed state used in production.
psql "$PROMVIEW_TEST_DATABASE_URL" -v ON_ERROR_STOP=1 -c 'DROP SCHEMA public CASCADE; CREATE SCHEMA public;'
PROMVIEW_DATABASE_URL="$PROMVIEW_TEST_DATABASE_URL" go run ./cmd/promview migrate
