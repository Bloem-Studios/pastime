# Configuration reference

The API and worker read environment variables at startup; they do not automatically load `.env`. For local development, source the file created by `make dev-env` (keep it private). Deployment uses the environment files described in the [installer guide](../deploy/lxc/README.md).

## Runtime and identity

| Variable | Default / behavior |
| --- | --- |
| `DATABASE_URL` | Required PostgreSQL URL with a host and database name; API/worker use runtime credentials, migrations use maintenance credentials |
| `LISTEN_ADDR` | `127.0.0.1:8080` |
| `PUBLIC_ORIGIN` | Inferred only for a loopback listener; otherwise required. HTTPS outside loopback; no path, query or embedded credentials |
| `DATABASE_MAX_CONNECTIONS` | 8 per process; allowed range 2–64 |
| `WORKER_COUNT` | 2; allowed range 1–16; must leave at least one pool connection free |
| `VALKEY_URL` | Optional `redis://` or `rediss://` URL for disposable cache state; PostgreSQL remains authoritative |
| `SECRET_KEY` | Optional base64 32-byte key; encrypted-secret features are unavailable without it |
| `SECRET_KEY_PREVIOUS` | Comma-separated previous keys accepted for decryption; new writes use the current key |
| `TRUSTED_PROXY_CIDRS` | Empty by default; forwarding headers ignored unless the socket peer is trusted |
| `OIDC_ISSUER`, `OIDC_CLIENT_ID`, `OIDC_CLIENT_SECRET` | Configure together to enable OIDC |
| `OIDC_NAME` | Optional display name for OIDC |
| `OIDC_AUTO_CREATE` | Only literal `true` enables automatic account creation; disabled by default |
| `PUBLIC_REGISTRATION` | Only literal `true` enables public local registration with member access; disabled by default. Email is contact metadata, not verified identity. |
| `NOTIFY_ALLOW_PRIVATE` | Only literal `true` allows notification delivery to private network destinations |
| `SERVICES_ALLOW_PRIVATE` | Only literal `true` allows members to link private-network services; administrators have separate permission |
| `SILO_SYNC_REQUESTS_PER_MINUTE` | 600; allowed range 60–100000. Requests per minute for each Silo sync key (one connected Silo profile); all sync traffic from one address has a ceiling of 6,000 per minute, and an address that sent 120 keys Pastime never issued in a minute is refused until the minute ends |

For proxy trust rules, key rotation and change-feed clients, see [development](development.md). Keep all key generations needed by retained ciphertext and backups. Configuring a destination is not proof of reachability or successful delivery.

## Metadata and linked services

| Variable | Purpose |
| --- | --- |
| `TMDB_API_KEY` | Movie/TV metadata; v3 key or v4 read token |
| `IGDB_CLIENT_ID`, `IGDB_CLIENT_SECRET` | IGDB/Twitch game metadata |
| `OPENCRITIC_API_KEY` | Optional RapidAPI key for cached game critic scores; background-only, 100 requests per calendar month |
| `COMICVINE_API_KEY` | Comic Vine metadata |
| `TVDB_API_KEY`, `TVDB_PIN` | TheTVDB v4 series and anime metadata; the PIN only for subscriber keys |
| `GCD_USERNAME`, `GCD_PASSWORD` | Optional Grand Comics Database account; the API also answers anonymously |
| `BGG_TOKEN` | BoardGameGeek metadata |
| `METADATA_LANGUAGE` | Provider language, default `en-US` |
| `PROVIDER_CONTACT` | Contact in the provider User-Agent |
| `TRAKT_CLIENT_ID`, `TRAKT_CLIENT_SECRET` | Trakt application credentials |
| `SIMKL_CLIENT_ID` | Simkl application credential |
| `MAL_CLIENT_ID` | MyAnimeList application credential |
| `LASTFM_API_KEY` | Last.fm application credential |
| `STEAM_API_KEY` | Steam application credential |

Open Library, Audnexus, AniList, MangaBaka, the Grand Comics Database, YouTube (oEmbed), MusicBrainz, iTunes and podcast RSS need no instance keys. Personal service credentials are entered in the application and stored encrypted with `SECRET_KEY`. Search runs on PostgreSQL; there is no external search engine.

See [features](features.md) for what each provider and linked service does, and for the OpenCritic quota.

## Year in Review notices

| Variable | Purpose |
| --- | --- |
| `REVIEW_NOTICES_URL` | Optional https address of the customer-notice service. Once a year, from 27 December, the worker sends it each customer's Year in Review summary (year, minutes and number of titles) so it can email them. Pastime stores no email addresses |
| `REVIEW_NOTICES_TOKEN` | Bearer token for that service |

Set both or neither. The address must be https and carry no credentials or query string; startup fails otherwise. Without them no notices are sent.

## Household and native Bloem tracking

Profile selection and native enrollment authority are stored in the database, not in environment variables. See [managed tracking](managed-tracking.md). Native provisioning credentials are for backend use only and are distinct from personal sessions and tokens. The command `pastime-bloem-register` reads the same runtime environment as the server.

## Development and verification tools

| Variable | Consumer / constraint |
| --- | --- |
| `POSTGRES_PASSWORD`, `POSTGRES_PORT`, `VALKEY_PORT` | Development Compose; generated `.env` uses loopback ports 18543 and 18679 |
| `PASTIME_TEST_DATABASE_URL` | Integration/browser setup; isolated loopback database named exactly `pastime_test` |
| `PASTIME_TEST_VALKEY_URL` | Integration fixtures; random expiring keys, no global flush |
| `PASTIME_REQUIRE_INTEGRATION` | `1` makes missing fixtures fail; set by `make test-integration` |
| `PASTIME_USABILITY_BASE_URL` | Selects the listener the usability browser suite tests; see [usability acceptance](../tests/browser/USABILITY.md) |
| `PASTIME_BROWSER_RUNTIME_DATABASE_URL` | Optional restricted-role browser API connection after maintenance setup; grants must already exist |
| `PERF_DATABASE_URL` | Store benchmark fixture; dedicated loopback `pastime_perf`; seeding is destructive to that fixture |
| `PERF_USER` | Benchmark account selector; default `perf_user_1`, or `perf_mega` for the large fixture |
| `PASTIME_LOAD_TOKEN` | Synthetic native API token for the loopback HTTP load harness |

Backup variables (`BACKUP_RECIPIENT`, `BACKUP_REMOTE`, `GNUPGHOME`, `RESTORE_CHECK_BINARY`, optional `RESTORE_POSTGRES_IMAGE`) and key custody are documented in [recovery](../deploy/lxc/RECOVERY.md).
