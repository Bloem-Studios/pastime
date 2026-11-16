#!/bin/sh
# Browser tests run against the isolated test database, never the development one.
set -eu
export DATABASE_URL="$PASTIME_TEST_DATABASE_URL"
# Tests never depend on local provider keys or live metadata services.
unset TMDB_API_KEY IGDB_CLIENT_ID IGDB_CLIENT_SECRET COMICVINE_API_KEY BGG_TOKEN OPENCRITIC_API_KEY
go build -o bin/pastime-e2e ./cmd/pastime
./bin/pastime-e2e migrate
printf '%s' 'synthetic-e2e-password-2026' | ./bin/pastime-e2e bootstrap-owner e2e_owner 2>/dev/null || true
# A rehearsal can use maintenance credentials for setup and a restricted API role.
export DATABASE_URL="${PASTIME_BROWSER_RUNTIME_DATABASE_URL:-$DATABASE_URL}"
exec ./bin/pastime-e2e serve
