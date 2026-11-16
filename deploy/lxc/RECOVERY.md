# Database privileges and recovery

See [installation and maintenance](README.md) for host layout and [configuration](../../docs/configuration.md) for runtime settings. A configured timer is not evidence of a successful backup.

The installer stops the worker and API before changing configuration or restarting dependencies, runs migration with the
root-only `/etc/pastime/migrate.env`, applies `runtime-role.sql`, then starts the
API and waits for readiness before starting workers. Neither runtime unit loads
migration credentials. `pastime_app` has table DML, sequence use and schema read
access; it cannot create schema objects or modify Goose's migration ledger.
The maintenance `pastime` database role remains privileged and is confined to
root-run migration and backup operations. Do not give its credentials to the API.

## Local dumps on hosts with their own off-host backups

When the container itself is copied off-host by the hypervisor's backup system,
`/etc/pastime/backup.env` may instead contain only `BACKUP_MODE=local`. The service
then keeps seven custom-format dumps in `/var/backups/pastime` (mode 0700, files
0600) and contacts no other host. The dumps are not encrypted and do not include
`SECRET_KEY`; both rely on the host backup also capturing `/etc/pastime`. Verify a
restore from the host backup before relying on it.

## Configure encrypted off-host backups

Install GnuPG and OpenSSH client tools on the backup host. Generate a recovery key
on a separate recovery machine. Keep its private key and recovery instructions
off-host; import only the public key into `/etc/pastime/backup-keyring` (root-owned,
mode 0700). Record the exact host package versions and licenses with the deployment
inventory. The rehearsal uses GnuPG 2.4.7; its notices are in
`third_party/host-tools/`.

Create root-only `/etc/pastime/backup.env` (mode 0600):

```sh
BACKUP_RECIPIENT=FULL_RECOVERY_KEY_FINGERPRINT
BACKUP_REMOTE=backup-user@backup-host:/protected/pastime
```

Provision a restricted SSH credential and verify the backup host's SSH host key.
The recovery fingerprint must be the complete 40- or 64-digit fingerprint. Do not
store the recovery private key on the application host.

`backup.sh` creates a custom PostgreSQL dump, all configured `SECRET_KEY`
generations, and a timestamp/binary-hash/schema-version manifest. It encrypts the
bundle before copying it off-host. Provider configuration is in the encrypted
database; plaintext provider environment variables and database credentials are
not added to the key escrow file. Temporary plaintext files are removed on exit.
The backup service fails when configuration, encryption or transfer fails. It
keeps seven completed local encrypted bundles and writes `last-offhost-success`
only after a successful transfer. Configure retention on the backup host too.
A local file or a configured timer alone is not proof of recoverability.

## Rehearse restoration

On an isolated recovery/test host with Docker and GnuPG, build a Linux static
checker for that host's container architecture:

```sh
CGO_ENABLED=0 GOOS=linux go build -o /tmp/pastime-restore-check ./tools/restorecheck
GNUPGHOME=/protected/recovery-keyring \
RESTORE_CHECK_BINARY=/tmp/pastime-restore-check \
  tools/restore-rehearsal.sh /protected/backups/pastime-TIMESTAMP.tar.gpg
```

The script decrypts and validates bundle members, creates a fresh task-owned
PostgreSQL container with **no network**, restores the dump, and runs the checker
inside it. It checks the embedded schema version, reads account/library/history/
collection tables and decrypts notification, linked-service, TOTP and pending
service-authorization secrets. It prints counts only. It never starts Pastime
workers or sends provider requests. The container, its anonymous volumes and the
temporary plaintext files are removed on both success and failure. The recovery
keyring is retained. An incorrect or missing application key makes verification
fail. Use the checker from the same application version as the backup; apply later
migrations only as a separate, rehearsed upgrade.

Record the date, encrypted bundle hash, PostgreSQL/application versions, counts
and exit status in your recovery records. The scripts are rehearsed with the pinned PostgreSQL 18 image, a restricted
runtime role and an application-written encrypted secret. That rehearsal does not replace a restore test on your own host and a real off-host copy.

The PostgreSQL 18 rehearsal mounts `/var/lib/postgresql` only and waits for the final TCP listener, avoiding the temporary initialization server. The checker validates notification secrets using the same purpose label as the application. Target-host acceptance and a real off-host copy still need your own testing.
