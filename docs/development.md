# Running Pastime locally

This page sets up a local development copy, including tracking and the administration UI. It is not a production recipe; see [deployment](../deploy/lxc/README.md) for that.

## Requirements

- Go (the version in `go.mod`; its `toolchain` line names the patched release Go will fetch), Node 26, pnpm 10.32.1, Python 3 and Docker Compose.
- Free loopback ports 18543 (development PostgreSQL), 18679 (Valkey), 8080 (manual API), and 18161 (browser tests). If changing ports, update the private environment consistently.

```sh
pnpm install --frozen-lockfile --ignore-scripts
go mod download
make web
make dev-env
make dev-up
set -a
. ./.env
set +a
go run ./cmd/pastime migrate
# Bootstrap once before starting the API; the password is read from stdin.
read -r -s -p "Initial owner password: " owner_password
printf '\n'
printf '%s' "$owner_password" | go run ./cmd/pastime bootstrap-owner owner
unset owner_password
go run ./cmd/pastime serve
```

`make dev-env` creates mode-0600 `.env` with a random development database password and refuses to overwrite an existing file. Never commit it. PostgreSQL has a named development volume; `make dev-down` stops only this Compose project and preserves its data.

The setup block uses Bash for the hidden password prompt. Open `http://127.0.0.1:8080/` and sign in as the owner you created. On later starts, skip environment creation and owner bootstrap. Sign in as an administrator to use `/docs/` and the native schemas. The API reports its stage as `preview`; Pastime has no stable release yet.

Liveness (`/healthz`) is independent of dependencies. Readiness (`/readyz`) requires PostgreSQL and the expected schema; optional Valkey failure reports degraded service. PostgreSQL owns authentication limits, jobs and durable state.

## Metadata providers

Keyless providers (Open Library, AniList, MusicBrainz, iTunes) are always enabled. Set these to enable the others; values are never logged or returned by the API:

| Variable | Enables |
|---|---|
| `TMDB_API_KEY` | Movies and TV (v3 key or v4 read token) |
| `IGDB_CLIENT_ID`, `IGDB_CLIENT_SECRET` | Games (Twitch application credential) |
| `COMICVINE_API_KEY` | Comics |
| `BGG_TOKEN` | Board games |
| `METADATA_LANGUAGE` | Provider language, default `en-US` |
| `PROVIDER_CONTACT` | Contact added to the User-Agent (MusicBrainz asks for one) |

Without a provider for a media type, search falls back to the local catalog and the UI says so. Run `make web` before `go run` so the embedded web app is current; `make build` does both.

## Durable worker probe

In another terminal with the same exported environment:

```sh
go run ./cmd/pastime worker
```

Enqueue a real internal database-connectivity job:

```sh
go run ./cmd/pastime enqueue-probe local-runtime-check
```

Reusing that key with identical intent returns the same job, not a new execution. The worker also handles catalog refreshes, scrobbles, imports, playback pushes, list syncs, notifications and exports; unknown kinds become blocked. With at least two workers, one lane is reserved for scrobbles and runtime probes. This is an operational queue test, not a fake provider import.

Inspect results with the development database:

```sh
docker compose --env-file .env -f compose.dev.yaml exec -T postgres \
  psql -U pastime -d pastime_dev \
  -c 'SELECT id, kind, status, attempts, last_error_code FROM jobs ORDER BY created_at DESC LIMIT 20;'
```

The queue supports transactional enqueue, deduplication, bounded claims/recovery, leases/heartbeats and fencing. Idempotent work can retry with backoff. Reconciliation-required work blocks after uncertain failure or an expired lease. There is no generic destructive retry/cleanup command.

## Tests and generation

Create the distinct test database once; an existing database causes this command to fail rather than being dropped/recreated:

```sh
make dev-test-db
make test-integration
make check-generated
make vet
pnpm exec playwright install chromium
pnpm test:browser
```

`make test-integration` limits Go package concurrency to two because parallel isolated-schema migrations can exhaust PostgreSQL’s default lock table on many-core hosts. For a deliberately more parallel run, provision only the disposable test database with a larger `max_locks_per_transaction` (this review used 512), restart it and recheck its bound port/readiness. Do not change production settings for a test run.

Integration tests require the exact database name `pastime_test` on loopback, validate its live identity, and create disposable schemas for synthetic fixtures. Do not point them at the development app database. Valkey tests use an expiring random test key and never flush an instance.

`make test` builds/typechecks the frontend, runs Node and installer-order tests, then Go race tests; unavailable integration fixtures can be skipped. `make test-integration` requires both explicit fixture URLs and refuses silent skips. The default browser config runs `app.spec.ts` and `swagger.spec.ts`. Managed-profile, usability and response-fixture regression specs use their own configs and require the synthetic accounts/listeners described in those files; they must not be selected by the default runner. Browser tests start their own loopback server and refuse to reuse an existing listener. They migrate and bootstrap with `PASTIME_TEST_DATABASE_URL`; optionally set `PASTIME_BROWSER_RUNTIME_DATABASE_URL` to an already-granted restricted role for the API process. See the [configuration reference](configuration.md).

```sh
make generate
make check-generated
make licenses
make build
make bench
```

Generation uses the independently authored OpenAPI specification to produce Go DTOs and TypeScript API types. Route/spec and response/schema tests cover the current operations. The React application consumes the generated TypeScript schema. `make check-generated` also verifies translation coverage; all four translated catalogs have 1,938 strings at this checkpoint.

The microbenchmark measures only in-process HTTP handler/recorder overhead. It excludes real networking, authentication, domain queries and representative datasets. It is not an end-to-end performance figure.

## Limits and release blockers

- Pools are bounded per process; budget connections across API and worker replicas.
- Migrations run explicitly. Development uses the fixture owner; the deployment installer separates maintenance and runtime roles, verified in the isolated rehearsal.
- Authentication, administration, tracking, provider adapters and disposable caches are implemented. Meilisearch and its durable indexing workflow remain planned.
- Native snapshots require an empty target and enforce the limits below. Private RSS feeds are unsupported.
- System backup tooling is implemented and locally rehearsed. The base host installation does not verify current-branch upgrades, off-host transfer/retention or disk-backed failure recovery.
- Provider compatibility has not been checked against live services. Current tests use synthetic fixtures.
- Pastime does not aim at full compatibility with the API of Floppy, the project that inspired it, and has no migration from it.

See [configuration](configuration.md).

## Review hardening configuration

`TRUSTED_PROXY_CIDRS` accepts comma-separated IP network prefixes. It is empty by default: forwarding headers are ignored. When the socket peer is trusted, the server walks `X-Forwarded-For` from right to left and stops at the first untrusted address. Configure the reverse proxy to append the actual peer address; do not trust every network.

`SECRET_KEY` is the current 32-byte base64 encryption key. `SECRET_KEY_PREVIOUS` accepts comma-separated previous keys for decryption during rotation. New writes include a key identifier and use the current key. Legacy ciphertext remains readable. Preserve old keys until all stored secrets have been rewritten and every backup that needs them has expired. Keys and backup escrow belong outside this repository.

Sync clients must capture `/api/v1/changes` **before** downloading the initial library, then replay changes from that cursor using ID-based upserts and tombstones. The per-profile feed lock orders sequence allocation with commits; distinct tracking profiles remain independent.

Scheduled exports clamp to the end of short months and restore the original preferred day in the following month. A persisted due-time receipt prevents duplicate stored files on job retries. An interrupted HTTP export aborts the transport; clients must treat network errors as an incomplete download.

Tests use synthetic fixtures only.

### HTTP load and failure checks

`go run ./tools/httpload` offers requests on a fixed schedule against a loopback
synthetic instance. Supply its test API token through `PASTIME_LOAD_TOKEN`; never
use a production token or database. Defaults alternate the library page and
statistics endpoints at 20 requests/second for 30 seconds. Use `-workload` with a
JSON array of `{ "method": "GET", "path": "/api/v1/library?limit=30", "status": 200 }`
records to describe the intended mix; write scenarios also accept a `body` string
and receive distinct command keys. Writes are only appropriate for disposable fixtures.

The report includes offered/completed/dropped arrivals, unexpected responses,
status counts, and p50/p95/p99 latency measured from the scheduled arrival time
(including queue delay). Drops count against the error budget; the command exits
nonzero above `-p99-ms` or `-max-error-fraction`. Document seed size, hardware,
versions, cache state, workload, rate and concurrency with every result. Run warmup
separately. The synthetic slow/503 test verifies overload accounting; it is not a
Pastime throughput measurement. Provider outages and worker shutdowns should be
injected only into task-owned fixtures and reported as separate scenarios.

Notification release claims and destination records now commit together. Failed or still-leased
HTTP deliveries remain pending and are retried separately from successful
recipients. Both notification and playback delivery are at least once: the remote
acknowledgement and PostgreSQL receipt cannot commit atomically. Playback receipts
are scoped to the linked configuration generation and use play creation time, so
backdated entries are eligible. Missing remote titles remain pending for a later
sync. Do not describe either integration as exactly-once delivery.

Unlisted list URLs now use a random share token, expire after 90 days and can be
renewed from the sharing panel. Renewal or private visibility revokes the previous
link. Existing guessable unlisted slug URLs stop working after migration; owners
must copy the new link. Public list pages, JSON, RSS and CSV have cursor pagination;
RSS/CSV expose continuation through the `Link` header. Public responses are not
cached so revocation is checked on every request. A frozen list copies membership
and list annotations into a new private manual list; it does not freeze shared
catalog metadata. Shared smart lists explicitly disclose that membership can
reveal the private rules used to select titles.

Collection pages return full filtered copy/title/spending totals alongside a
bounded page. Unknown currencies remain separate. Statistics report plays with
unknown runtimes separately from summed minutes. Catalog remaps preview versioned
effects and keep unmatched plays with their original catalog identity; those facts
remain in history and timed statistics but do not complete the newly matched title.
Deployment/recovery procedures are in `deploy/lxc/RECOVERY.md`.

### Native transfers and integration boundaries

Native JSONL exports now use version 3 (restore also accepts version 2) and require both `library:read` and `lists:read`;
native imports require `library:write` and `lists:write`. They include manual and catalog-linked entries,
cycles/dates, edition selections, plays (including unmapped plays and runtime evidence),
skips, copies/custom fields, tags, lists/membership notes, activity and resume positions.
A manifest identifies the source namespace; a SHA-256 trailer verifies completeness.
Restore commits content, its replay receipt and the terminal import outcome atomically, requires an empty target library/organization, maps all personal IDs,
and keeps restored lists private. It restores library content, not credentials, shared
collaborators, public share links or external sync configuration. Catalog identities are
resolved through normal provider paths; unavailable references or episode orders fail
without partially restoring personal data. The same file can be retried without
repeating its effects. Restore does not overwrite subsequent edits. Legacy entry-only
exports remain supported, but legacy files containing manual entries fail explicitly
because those files omitted fields needed for a complete restore.

JSONL output is streamed row by row, with limits of 64 MiB and 100,000 records. Native
uploads also accept gzip with the same decompressed limit. ZIP imports reject duplicate
basenames, more than 1,000 files, and expansion above 64 MiB; CSV parsing caps rows at
100,000. The legacy CSV/Letterboxd export path rejects a 500-entry batch above 100,000
plays instead of exhausting memory. Limits return failures, never successful truncation.

Linked playback pushes run independently from inbound imports. Per-play delivery
receipts survive queue retries, and the integration UI shows the latest push job status.
Remote delivery is at least once: a crash after the remote side accepts a request but
before PostgreSQL acknowledges it can repeat the remote effect. Webhooks with stable
`event_id` or `X-Event-ID` use permanent replay receipts; events without an identifier
retain the three-hour duplicate heuristic. Replacing a webhook connection or changing
its filters invalidates queued work from its previous configuration.

RSS addresses enter the shared catalog only after that exact address appears in an
Apple podcast-directory record. Search the public podcast by name first. Private feed
addresses are unsupported: neither stripping query parameters nor guessing token-like
paths is a sufficient privacy boundary. Reader sync rejects older timestamps and
conflicting equal-timestamp positions; an explicitly newer timestamp permits a rewind.
Legacy reader clients that omit timestamps retain arrival-order behavior.

See [recovery procedures](../deploy/lxc/RECOVERY.md)
for encrypted database/key bundles, off-host copies, isolated restore checks,
and the maintenance/runtime database-role split. Backup health is unverified until
you configure and demonstrate off-host recovery on your installation.

See [September capabilities](september-capabilities.md) for the new personal-data snapshot fields, provider adapters, background bounds and supported import columns.
