#!/bin/sh
# Installs or upgrades Pastime inside a Debian container that has Docker.
# Run as root from the directory holding these files and a linux `pastime` binary:
#   PUBLIC_ORIGIN=https://host.example [TMDB_API_KEY=...] ./install.sh
# Secrets are generated on first install and kept in /etc/pastime (root and the
# pastime user only); later runs keep them and only replace the binary and units.
set -eu
: "${PUBLIC_ORIGIN:?PUBLIC_ORIGIN is required}"
here=$(cd "$(dirname "$0")" && pwd)

# A warm-standby copy (standby-controller installed) keeps its database on the external
# cluster named in migrate.env and pastime.env, its local Postgres stopped as a rollback copy,
# and its units started only by standby-controller; standby-sync copies the result to the peer.
# Install only on the active copy and leave that layout alone.
standby=0
if [ -f /etc/standby-controller.json ]; then
	state=$(cat /run/standby-controller/state 2>/dev/null || true)
	if [ "$state" != active ]; then
		echo "this warm-standby copy is '${state:-unknown}', not active: install on the active copy" >&2
		exit 1
	fi
	standby=1
fi

id pastime >/dev/null 2>&1 || useradd --system --home-dir /nonexistent --shell /usr/sbin/nologin pastime
install -d -m 0755 /opt/pastime /var/lib/pastime
install -d -m 0750 -g pastime /etc/pastime
install -d -m 0700 /var/backups/pastime
# Drain the old worker and API before replacing configuration, restarting dependencies or changing the schema.
# A failed migration leaves both stopped for operator inspection.
for unit in pastime-worker.service pastime.service; do
 if systemctl cat "$unit" >/dev/null 2>&1; then systemctl stop "$unit"; fi
done

install -m 0644 "$here/compose.yaml" "$here/postgresql.conf" "$here/runtime-role.sql" /opt/pastime/
install -m 0755 "$here/backup.sh" /opt/pastime/

random() { head -c "$1" /dev/urandom | base64 | tr -d '\n'; }
if [ ! -f /etc/pastime/compose.env ]; then
	umask 077
	printf 'POSTGRES_PASSWORD=%s\n' "$(random 24 | tr '+/' '-_' | tr -d '=')" > /etc/pastime/compose.env
fi
. /etc/pastime/compose.env
if [ ! -f /etc/pastime/pastime.env ]; then
	umask 027
	{
		printf 'DATABASE_URL=postgres://pastime:%s@127.0.0.1:5432/pastime?sslmode=disable\n' "$POSTGRES_PASSWORD"
		printf 'VALKEY_URL=redis://127.0.0.1:6379/0\n'
		printf 'LISTEN_ADDR=127.0.0.1:8080\n'
		printf 'DATABASE_MAX_CONNECTIONS=32\n'
		printf 'WORKER_COUNT=4\n'
		printf 'SECRET_KEY=%s\n' "$(random 32)"
	} > /etc/pastime/pastime.env
	chgrp pastime /etc/pastime/pastime.env
fi
# Migration credentials are root-only and never loaded into either runtime service.
if [ ! -f /etc/pastime/migrate.env ]; then
 umask 077
 printf 'DATABASE_URL=postgres://pastime:%s@127.0.0.1:5432/pastime?sslmode=disable\n' "$POSTGRES_PASSWORD" > /etc/pastime/migrate.env
fi
if [ ! -f /etc/pastime/runtime.env ]; then
 umask 077
 printf 'RUNTIME_DB_PASSWORD=%s\n' "$(random 24 | tr '+/' '-_' | tr -d '=')" > /etc/pastime/runtime.env
fi
. /etc/pastime/runtime.env
# Settings passed on the command line replace their stored values.
set_env() {
	[ -n "$2" ] || return 0
	grep -v "^$1=" /etc/pastime/pastime.env > /etc/pastime/pastime.env.new || true
	printf '%s=%s\n' "$1" "$2" >> /etc/pastime/pastime.env.new
	chmod 0640 /etc/pastime/pastime.env.new
	chgrp pastime /etc/pastime/pastime.env.new
	mv /etc/pastime/pastime.env.new /etc/pastime/pastime.env
}
[ "$standby" = 1 ] || set_env DATABASE_URL "postgres://pastime_app:$RUNTIME_DB_PASSWORD@127.0.0.1:5432/pastime?sslmode=disable"
set_env PUBLIC_ORIGIN "$PUBLIC_ORIGIN"
set_env TMDB_API_KEY "${TMDB_API_KEY:-}"

cd /opt/pastime
[ "$standby" = 1 ] || docker compose --env-file /etc/pastime/compose.env up -d --wait

install -m 0755 "$here/pastime" /usr/local/bin/pastime.new
mv /usr/local/bin/pastime.new /usr/local/bin/pastime
install -m 0644 "$here/pastime.service" "$here/pastime-worker.service" "$here/pastime-backup.service" "$here/pastime-backup.timer" /etc/systemd/system/
systemctl daemon-reload
# The backup timer is installed but not enabled: an operator enables it once a backup
# destination is configured (RECOVERY.md). Upgrades leave its current state alone.
[ "$standby" = 1 ] || systemctl enable pastime.service pastime-worker.service
systemd-run --quiet --wait --collect --property=User=root --property=EnvironmentFile=/etc/pastime/migrate.env /usr/local/bin/pastime migrate
# Password is generated from URL-safe characters, so it cannot escape the psql literal.
if [ "$standby" = 0 ]; then
{
 printf "\\set runtime_password '%s'\n" "$RUNTIME_DB_PASSWORD"
 cat /opt/pastime/runtime-role.sql
} | docker compose --env-file /etc/pastime/compose.env exec -T postgres psql -X -U pastime -d pastime >/dev/null
fi
systemctl start pastime.service

for _ in $(seq 1 30); do
	if curl -fsS http://127.0.0.1:8080/readyz >/dev/null 2>&1; then
		systemctl start pastime-worker.service
		echo "pastime is ready"
		exit 0
	fi
	sleep 1
done
echo "pastime did not become ready" >&2
journalctl -u pastime.service -n 40 --no-pager >&2
exit 1
