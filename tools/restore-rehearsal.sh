#!/bin/sh
# Decrypt and restore a bundle into a new disposable container on an isolated network.
# Run on a recovery/test host, with the recovery private key in GNUPGHOME.
set -eu
umask 077
: "${GNUPGHOME:?set the recovery keyring directory}"
: "${RESTORE_CHECK_BINARY:?build tools/restorecheck and give its absolute path}"
[ "$#" -eq 1 ] || { echo 'usage: restore-rehearsal.sh encrypted-bundle.tar.gpg' >&2; exit 2; }
image=${RESTORE_POSTGRES_IMAGE:-postgres@sha256:9a8afca54e7861fd90fab5fdf4c42477a6b1cb7d293595148e674e0a3181de15}
stage=$(mktemp -d)
name="pastime-restore-check-$$"
cid=''
cleanup() {
 [ -z "$cid" ] || docker rm -fv "$cid" >/dev/null
 rm -rf -- "$stage"
}
trap cleanup EXIT HUP INT TERM
gpg --batch --yes --output "$stage/bundle.tar" --decrypt "$1"
printf '%s\n' database.dump keys.env manifest.txt > "$stage/expected"
tar -tf "$stage/bundle.tar" | LC_ALL=C sort > "$stage/actual"
cmp -s "$stage/expected" "$stage/actual" || { echo 'Unexpected recovery bundle members' >&2; exit 1; }
# Only regular files are allowed; no symlinks, hard links, directories or special files.
tar -tvf "$stage/bundle.tar" > "$stage/types"
if grep -v '^-' "$stage/types" >/dev/null; then echo 'Unsafe recovery bundle member' >&2; exit 1; fi
mkdir "$stage/data"
tar --no-same-owner --no-same-permissions -xf "$stage/bundle.tar" -C "$stage/data"
password=$(head -c 24 /dev/urandom | base64 | tr '+/' '-_' | tr -d '=\n')
cid=$(docker run -d --name "$name" --label pastime.task=restore-rehearsal --network none --tmpfs /var/lib/postgresql -e POSTGRES_USER=pastime -e POSTGRES_DB=pastime_restore_check -e "POSTGRES_PASSWORD=$password" "$image")
# The bootstrap server listens only on a socket and shuts down after initdb.
# Wait for the final TCP listener before restoring.
ready=false
for _ in $(seq 1 30); do
 if docker exec "$cid" pg_isready -h 127.0.0.1 -U pastime -d pastime_restore_check >/dev/null 2>&1; then ready=true;break;fi
 sleep 1
done
[ "$ready" = true ] || { echo 'Rehearsal database did not start' >&2; docker logs "$cid" >&2; exit 1; }
docker exec -i "$cid" pg_restore --exit-on-error --clean --if-exists --no-owner --no-privileges -U pastime -d pastime_restore_check < "$stage/data/database.dump"
docker cp "$RESTORE_CHECK_BINARY" "$cid:/tmp/restorecheck"
docker cp "$stage/data/keys.env" "$cid:/tmp/keys.env"
docker exec -e "RESTORE_DATABASE_URL=postgres://pastime:$password@127.0.0.1:5432/pastime_restore_check?sslmode=disable" "$cid" /tmp/restorecheck /tmp/keys.env
