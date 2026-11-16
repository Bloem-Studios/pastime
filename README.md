# Pastime

> [!CAUTION]
> **Offered without support.** Pastime is provided as-is, with no support of any kind. Issues are turned off, questions go unanswered, and pull requests are not accepted: they are closed without review. You are free to fork it under the AGPL v3.

Pastime is a self-hosted media tracker for what you watch, read, play and listen to: films, shows, anime, books, manga, comics, games, board games, podcasts, audiobooks, videos and music. It keeps a personal library with status, ratings, notes, episode progress, repeat cycles and history, plus lists, statistics, release calendars and imports from other trackers. One installation can serve a household, with a separate tracking profile for each person. It is for people who want to host their own tracker. It also links to [Bloem Server](https://github.com/Bloem-Studios/bloem-server), which is built on the open-source [Silo](https://github.com/Silo-Server/silo-server) media server, so that what a profile watches there is recorded here.

Pastime has no stable release yet. The API reports its stage as `preview`, and the schema may change between versions. Back up before every upgrade.

It is written in Go (chi, pgx, Goose) with a React and TypeScript web app that is embedded in the binary. PostgreSQL holds all durable state. Valkey is an optional cache and can be lost at any time.

## Features

The detailed reference is [docs/features.md](docs/features.md).

**Tracking**
- Twelve media types: movies, series, anime, books, manga, comics, games, board games, podcasts, music, audiobooks and videos.
- Status, progress, repeat cycles, skipped episodes, play history, check-ins that finish by themselves when a film or episode ends, and a status history for each title.
- Ratings on a 10, 5 or 100 scale (or off), with dates, plus ratings for single episodes.
- Tags, owned copies (physical or digital) with custom fields, editions, alternate episode orders, your own cover art, and a "Correct the match" tool that moves a title to another catalog record or provider.
- Saved library views, bulk actions on up to 200 titles, a configurable Home page and Discover suggestions.

**Lists and sharing**
- Manual and smart lists, tier boards that export as an image, and collaborative lists with editors and viewers.
- Private, unlisted (expiring link) and public lists, with JSON, RSS and CSV output, and an optional public profile.
- Lists that follow a public Trakt or MDBList list, and export of a list to Trakt.
- Recommendations between people on the same server.

**Metadata**
- TMDB, TheTVDB, AniList, MangaBaka, Open Library, Audnexus, IGDB, BoardGameGeek, Comic Vine, the Grand Comics Database, MusicBrainz, iTunes, podcast RSS and YouTube. Providers that need a key are off until you set one. Without a provider, search uses the local catalog.
- Cast, crew and studio pages, regional movie release dates and cached OpenCritic scores for games (optional).

**Calendar, statistics and notices**
- A release calendar with a private `.ics` subscription link.
- Release notices to webhooks, ntfy, Gotify, Discord or Apprise.
- Statistics (time, streaks, genres, people, studios, creators, ratings) and a Year in Review for any year.

**Imports, exports and linked services**
- File imports from Trakt, Letterboxd, IMDb, Goodreads, StoryGraph, AniList, Yamtrack, Grouvee, TV Time, CLZ, Playnite, WeTrakr and generic CSV, with an unmatched-titles review and rollback.
- Linked services that sync on a schedule: Trakt, Simkl, MyAnimeList, AniList, Kitsu, Hardcover, Steam, PlayStation, Xbox, BoardGameGeek, Jellyfin, Emby, Plex, Audiobookshelf, Last.fm, ListenBrainz, Koito, gPodder, Pocket Casts, Stremio, Storyteller, Radarr, Sonarr, Komga, Kavita, Mylar3 and Kapowarr.
- Webhooks from Plex, Jellyfin, Emby, Kodi and Overseerr or Jellyseerr; a ListenBrainz-compatible endpoint for Navidrome; a KOReader sync server.
- Native JSONL export and restore, CSV and Letterboxd exports, and scheduled snapshots.

**Accounts and administration**
- Local accounts with invitations, optional public registration, OpenID Connect sign-in, authenticator-based password recovery, sessions, scoped API tokens and device-code sign-in for TV and phone apps.
- Household profiles: several tracking profiles under one account, each with its own library, and optional profile logins.
- An administration console with a dashboard, jobs, providers, catalog, imports, users, invitations, an audit log and configuration checks.
- An OpenAPI description with a Swagger UI at `/docs/` for administrators, and an MCP endpoint for AI assistants.

**Bloem Server**
- Watch sync, favourites, watchlist and ratings with Bloem Server through the Pastime plugin, household profile provisioning, and Bloem availability, calendar and recommendations shown in Pastime. See [Connecting to Bloem Server](#connecting-to-bloem-server).

**Platform**
- A responsive web app in English, German, French, Dutch and Spanish, embedded in a single Go binary.
- PostgreSQL for all durable state, including the job queue. Valkey is an optional cache.
- Encrypted off-host backup scripts and a restore check.

Adapters are tested with synthetic fixtures built from each service's published API, so a passing test does not show that a live service still behaves the same way.

## Running it

Pastime needs PostgreSQL 18 (the installer pins 18.4) and, optionally, Valkey. There is no container image of Pastime itself. The repository supports two ways to run it.

### On a Debian host (the installer)

[`deploy/lxc/`](deploy/lxc/README.md) installs Pastime on a dedicated Debian host with Docker Compose and systemd. PostgreSQL and Valkey run in Docker bound to loopback, and Pastime runs as two systemd services (web and worker). The host needs Docker Compose, systemd and curl.

1. Build the binary on a machine with Go, Node 26 and pnpm (see [Building](#building-and-testing)): `make build` writes `bin/pastime`.
2. Copy the files in `deploy/lxc/` and the binary (named `pastime`) to the host and, as root in that directory, run:

   ```sh
   PUBLIC_ORIGIN=https://pastime.example.com ./install.sh
   ```

   Add `TMDB_API_KEY=...` to set a TMDB key at the same time. The installer generates the database and encryption secrets under `/etc/pastime`, migrates the database with a maintenance role, starts the web service on `127.0.0.1:8080`, checks `/readyz`, and then starts the worker.
3. Put an HTTPS reverse proxy in front of `127.0.0.1:8080` for `PUBLIC_ORIGIN`. The installer does not set up DNS, TLS or a firewall. Set `TRUSTED_PROXY_CIDRS` in `/etc/pastime/pastime.env` to your proxy's address so that per-address rate limits see real client addresses, then restart the services.
4. Create the owner once, with the runtime environment loaded and the password on standard input:

   ```sh
   set -a; . /etc/pastime/pastime.env; set +a
   printf '%s' "$OWNER_PASSWORD" | pastime bootstrap-owner owner
   ```

   Then sign in at your `PUBLIC_ORIGIN`.

Encrypted off-host backups need GnuPG and OpenSSH, and are not enabled by the installer. See [recovery](deploy/lxc/RECOVERY.md).

### Locally, for development

```sh
pnpm install --frozen-lockfile --ignore-scripts
make web
make dev-env      # writes a private .env with random development passwords
make dev-up       # PostgreSQL and Valkey in Docker on loopback ports
set -a; . ./.env; set +a
go run ./cmd/pastime migrate
printf '%s' "choose-a-password" | go run ./cmd/pastime bootstrap-owner owner
go run ./cmd/pastime serve      # http://127.0.0.1:8080/
go run ./cmd/pastime worker     # in a second terminal, with the same environment
```

[docs/development.md](docs/development.md) has the details.

### Commands

`pastime serve`, `worker`, `migrate`, `bootstrap-owner <username>`, `enqueue-probe [key]`, `licenses` (prints the embedded third-party notices) and `review-notices check [year]`. A second program, `cmd/pastime-bloem-register`, registers a Bloem Server (see below).

## Configuration

Pastime reads environment variables at startup; it does not load a `.env` file by itself. The full table, with defaults and ranges, is in [docs/configuration.md](docs/configuration.md). The ones you will normally set:

| Variable | Meaning |
| --- | --- |
| `DATABASE_URL` | Required. PostgreSQL URL with a database name. |
| `PUBLIC_ORIGIN` | The HTTPS address people use, for example `https://pastime.example.com`. Required unless the listener is loopback. |
| `LISTEN_ADDR` | Default `127.0.0.1:8080`. |
| `VALKEY_URL` | Optional `redis://` or `rediss://` cache. |
| `SECRET_KEY` | Base64 32-byte key that encrypts stored secrets (linked-service tokens, notification targets). Keep every key your data or backups still need; `SECRET_KEY_PREVIOUS` lists old ones. |
| `TRUSTED_PROXY_CIDRS` | Proxy networks whose forwarding headers are trusted. Empty by default. |
| `OIDC_ISSUER`, `OIDC_CLIENT_ID`, `OIDC_CLIENT_SECRET` | Set all three to enable OpenID Connect sign-in. |
| `PUBLIC_REGISTRATION` | `true` allows self-registration as a member. Off by default. |
| `TMDB_API_KEY` and other provider keys | Enable the matching metadata or linked-service features. |

## Connecting to Bloem Server

Bloem Server reaches Pastime through the Bloem Pastime plugin, [bloem-plugin-pastime](https://github.com/Bloem-Studios/bloem-plugin-pastime), which is installed from the [Bloem plugin catalog](https://github.com/Bloem-Studios/bloem-plugins). The plugin sends each profile's playback, finished plays, favourites, watchlist and ratings to Pastime, and brings back what Pastime recorded elsewhere. The plugin's own README lists exactly what is synced.

Setting it up, in order:

1. In the Bloem Server admin, install the plugin and set its **Pastime address** to your `PUBLIC_ORIGIN` (for example `https://pastime.example.com`).
2. Register the Bloem Server with Pastime once. On the Pastime host, as root and with the runtime environment loaded (`set -a; . /etc/pastime/pastime.env; set +a`), run `pastime-bloem-register` with an owner or administrator account (the password is read from a pipe on standard input):

   ```sh
   printf '%s' "$ADMIN_PASSWORD" | ./pastime-bloem-register -administrator owner \
     -instance my-bloem -scope household -source https://bloem.example.com \
     -output /root/pastime-bloem-registration.json
   ```

   Build the program with `go build -o pastime-bloem-register ./cmd/pastime-bloem-register` on your build machine and copy it over. `-instance` and `-scope` are names you choose. `-source` is your Bloem Server's HTTPS address. The output file is new and mode 0600, and holds a provisioning credential that is shown nowhere else. Run it without arguments to see the other modes (identity issuers, claims).
3. Each person opens **Connect Silo** in Pastime, creates a key for their tracking profile, and pastes it into Bloem under **Settings, Watch Providers, Pastime**. (Pastime's pages say "Silo" because Bloem Server speaks Silo's watch-sync contract.) A key lasts 90 days from its last renewal and renews itself while it keeps syncing.

How the routes, keys and rate limits work: [docs/integrations/silo.md](docs/integrations/silo.md). Household profiles and identity linking: [docs/managed-tracking.md](docs/managed-tracking.md).

## Building and testing

Requirements: Go (the version in `go.mod`), Node 26, pnpm 10.32.1, Python 3, and Docker Compose for the integration tests.

```sh
pnpm install --frozen-lockfile --ignore-scripts
make build                 # type-checks and builds the web app, then bin/pastime
make vet                   # go vet ./...
make test                  # web build, Node unit tests, installer tests, go test -race ./...
make check-generated       # OpenAPI-generated code and translation coverage
```

`make test` skips the tests that need a database. For the full suite, start the development services (`make dev-up`, then `make dev-test-db`), export `PASTIME_TEST_DATABASE_URL` and `PASTIME_TEST_VALKEY_URL` as described in [docs/development.md](docs/development.md), and run `make test-integration`. Browser tests are `pnpm exec playwright install chromium` and `pnpm test:browser`. The integration tests need a database named `pastime_test` on loopback and refuse anything else.

## Security

Report vulnerabilities privately through GitHub's private vulnerability reporting: <https://github.com/Bloem-Studios/pastime/security/advisories/new>. Please do not open a public issue for a security problem. Because the project is offered without support, a report may not get a reply.

## Licence

Pastime is licensed under the GNU Affero General Public License, version 3 only (AGPL-3.0-only). See [LICENSE](LICENSE). Dependencies and bundled assets keep their own licences: see [third_party/README.md](third_party/README.md), `third_party/NOTICES.txt`, and `pastime licenses`. The [licence and authorship notes](docs/provenance.md) describe how the code relates to Floppy and Silo, whose design informed it.
