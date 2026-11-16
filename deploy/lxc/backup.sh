#!/bin/sh
# Encrypt the database and its application encryption keys before off-host transfer.
# The host holds only the recovery public key; keep its private key off-host.
set -eu
umask 077
. /etc/pastime/backup.env
dir=/var/backups/pastime
cd /opt/pastime
# Local mode is for hosts whose own backups (for example container snapshots) already
# copy this directory off-host. It keeps seven plain custom-format dumps, readable only
# by root, and never contacts another host.
if [ "${BACKUP_MODE:-encrypted}" = local ]; then
 name="pastime-$(date -u +%Y%m%dT%H%M%SZ).dump"
 docker compose --env-file /etc/pastime/compose.env exec -T postgres pg_dump -U pastime -Fc pastime > "$dir/$name.part"
 mv "$dir/$name.part" "$dir/$name"
 ls -1t "$dir"/pastime-*.dump | tail -n +8 | while IFS= read -r old; do rm -- "$old"; done
 exit 0
fi
: "${BACKUP_RECIPIENT:?set the recovery key fingerprint}"
: "${BACKUP_REMOTE:?set user@backup-host:/absolute/directory}"
case "$BACKUP_RECIPIENT" in *[!A-Fa-f0-9]*|'') echo 'Use a full recovery key fingerprint' >&2; exit 1;; esac
[ "${#BACKUP_RECIPIENT}" -eq 40 ] || [ "${#BACKUP_RECIPIENT}" -eq 64 ] || exit 1
case "$BACKUP_REMOTE" in *@*:/?*) ;; *) echo 'BACKUP_REMOTE must name an off-host user@host:/absolute/directory' >&2; exit 1;; esac
export GNUPGHOME=/etc/pastime/backup-keyring
stage=$(mktemp -d "$dir/.stage.XXXXXXXX")
trap 'rm -rf -- "$stage"' EXIT HUP INT TERM
name="pastime-$(date -u +%Y%m%dT%H%M%SZ).tar.gpg"
docker compose --env-file /etc/pastime/compose.env exec -T postgres pg_dump -U pastime -Fc pastime > "$stage/database.dump"
# Include all decryption generations, without provider activation or database credentials.
awk '/^SECRET_KEY=|^SECRET_KEY_PREVIOUS=/' /etc/pastime/pastime.env > "$stage/keys.env"
{
 date -u +'%Y-%m-%dT%H:%M:%SZ'
 sha256sum /usr/local/bin/pastime
 docker compose --env-file /etc/pastime/compose.env exec -T postgres psql -X -At -U pastime -d pastime -c 'SELECT version_id FROM goose_db_version ORDER BY id DESC LIMIT 1'
} > "$stage/manifest.txt"
tar -C "$stage" -cf "$stage/bundle.tar" database.dump keys.env manifest.txt
gpg --batch --yes --trust-model always --recipient "$BACKUP_RECIPIENT" --output "$dir/$name.part" --encrypt "$stage/bundle.tar"
mv "$dir/$name.part" "$dir/$name"
# A failed copy leaves the encrypted local backup and makes the service fail.
scp -q -- "$dir/$name" "$BACKUP_REMOTE/"
{
 date -u +'%Y-%m-%dT%H:%M:%SZ'
 sha256sum "$dir/$name"
} > "$dir/last-offhost-success.part"
mv "$dir/last-offhost-success.part" "$dir/last-offhost-success"
# Prune only completed encrypted bundles, after successful off-host transfer.
ls -1t "$dir"/pastime-*.tar.gpg | tail -n +8 | while IFS= read -r old; do rm -- "$old"; done
