# Single-host installation and maintenance

These files install Pastime on a dedicated Debian host (a container or a virtual machine) with Docker Compose and systemd. PostgreSQL and Valkey run in Docker; the Pastime binary runs under systemd. Rehearse every installation and upgrade on a copy before using it on data you care about.

## Layout and prerequisites

Build `bin/pastime` for the target Linux architecture with `make build`, then place a copy named `pastime` beside `install.sh`, `compose.yaml`, `postgresql.conf`, the systemd units, `runtime-role.sql` and `backup.sh`. The host needs Docker Compose, systemd, curl and standard shell/coreutils tools. Encrypted off-host backups additionally need GnuPG, OpenSSH and an independently provisioned recovery key and destination.

The API binary and worker run as the `pastime` operating-system user. PostgreSQL and Valkey bind only to loopback. A separately configured HTTPS reverse proxy terminates the public origin; the installer does not configure DNS, TLS or firewall rules. This layout reserves local ports 5432, 6379 and 8080 and must not be installed over unrelated services.

| Path | Purpose |
| --- | --- |
| `/usr/local/bin/pastime` | API/worker/migration binary with embedded web assets |
| `/opt/pastime` | Compose, PostgreSQL configuration, role grants and backup script |
| `/etc/pastime/compose.env` | Root-only PostgreSQL bootstrap credentials |
| `/etc/pastime/migrate.env` | Root-only maintenance database URL |
| `/etc/pastime/runtime.env` | Root-only runtime database password used by the installer |
| `/etc/pastime/pastime.env` | Runtime configuration, readable by the service group; includes application encryption keys |
| `/var/lib/pastime/postgres` | PostgreSQL 18 parent data directory mounted at `/var/lib/postgresql` |
| `/var/backups/pastime` | Encrypted local bundles and off-host success observation |

## Installer behavior

As root, run `PUBLIC_ORIGIN=https://pastime.example.com ./install.sh`. Existing generated secrets are retained. Nonempty `PUBLIC_ORIGIN` and optional `TMDB_API_KEY` arguments replace their stored settings; other [configuration](../../docs/configuration.md) is managed in the runtime environment file.

The installer stops the old worker and API before replacing configuration or starting dependencies, installs the binary/units, migrates with maintenance credentials, grants the runtime role, starts the API, checks `/readyz`, then starts workers. Migration failure leaves the runtime services stopped for inspection. On a warm-standby copy (with `/etc/standby-controller.json`) it runs only on the active copy, refuses on the standby, and leaves `DATABASE_URL`, the local Compose Postgres, the runtime grant and unit enablement alone; standby-sync copies the result to the peer. It does not implement automatic binary/schema rollback. Back up first and rehearse the exact upgrade; a PostgreSQL major-version change requires its own database upgrade procedure.

Bootstrap an owner once on an empty installation using `pastime bootstrap-owner <username>` with the password on standard input and the intended runtime environment loaded. The installer does not create an owner or enable live provider accounts. `/docs/` and OpenAPI downloads require an administrator session.

Both runtime units use `pastime.env`; neither loads `migrate.env`. Application roles can perform table DML and read the schema ledger, but cannot create schema objects or modify the ledger. Budget database connections across API and worker processes.

## Backups and acceptance

The installer installs the backup timer but does not enable it. Enable `pastime-backup.timer` only after backup configuration and a recovery test. An enabled timer alone is not a configured or verified backup. Follow [RECOVERY.md](RECOVERY.md) to provision public-key encryption, an off-host SSH destination, retention and a recovery private key kept off-host. Missing configuration makes the backup service fail. On hosts whose own backups already copy the container off-host, `BACKUP_MODE=local` keeps plain local dumps instead; see [RECOVERY.md](RECOVERY.md).

Before relying on an installation, verify service drain/restart on the target systemd host, fresh owner bootstrap, HTTPS/proxy configuration, runtime privileges, real off-host transfer and restoration, and disk-backed failure recovery. The documented local rehearsal used synthetic data and disposable storage; it does not certify these operational checks.
