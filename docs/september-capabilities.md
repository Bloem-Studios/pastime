# September capabilities

Implemented independently following a behavioural review of Floppy through commit `da4fa955b1d001ac09d47e183b2e56af2909ca13`. This is a native implementation, not source parity. No Floppy/Silo code, assets, tests or Git history were imported.

## Library and personal evidence

- **Saved views:** Library → Saved views stores filters, sort order and grid/list layout. Views appear in navigation and can be edited, reordered or deleted. Each account has up to 100; updates/deletes require the current version. Reordering requires the full current set. Views remain private.
- **Rating dates:** `scored_at` records actual rating changes, including clearing a rating. Unrelated edits and replay do not restamp it. Existing unknown dates remain unknown; supported dated imports and native restores preserve supplied historical evidence.
- **Episode ratings:** Rate episodes from a series title or library detail; since the [October additions](october-capabilities.md) an episode need not be watched. The derived mean counts each rated episode once across repeat cycles. Unrated episodes do not enter the mean; the UI shows rated and watched counts. Season selection filters the aggregate. This does not overwrite your explicit series rating.
- **Status history:** Detail pages show previous/new status, timestamp and source/operation. History begins when migration 28 is applied; earlier changes are not invented.
- **Intent protection:** Automatic imports and scrobbles preserve paused, dropped and rated-only states. Automatic reprojection preserves explicitly completed titles. Automatic show/anime completion requires a positive terminal production status. Deleting a catalog entry records a durable marker; later bulk sync and request planning cannot recreate it. Explicitly adding it again clears the marker. A genuinely newer completed scrobble may recreate it; older playback cannot.
- **Collection search:** Owned supports a title/issue/edition query with private ownership checks, cursor pagination and filtered totals. Library's existing owned filter and shared cards continue to work for collection-only use without watch history.

Native exports now write **version 3** and include saved views, rating timestamps, episode ratings, status history and deletion markers. Restore accepts versions 2 and 3 into an empty personal library/organization; imported list sharing stays private. Credentials and integration configuration remain excluded. Limits remain 64 MiB and 100,000 records.

## Mylar3

Under Integrations → Linked services, supply the Mylar3 base URL and API key. The default schedule in its connection dialog is every two hours. Keys use the existing encrypted service-link storage and private-network policy. Comic Vine metadata must be configured to resolve the series.

The adapter calls read-only `getIndex` and `getComic` commands. Only **Downloaded** and **Archived** issues/annuals become digital owned copies. Issue and annual identities are separate. Where Comic Vine resolves a child issue, the copy references it; otherwise its issue/annual designation remains attached to the volume copy. Ownership does not imply that the comic was read.

A complete scan is staged durably before application. Only a fully successful, generation-matching run removes missing **Mylar3-owned** copies. Fetch failures, cancellation, unmatched items and partial application do not reconcile. Disconnect removes that source's copies; manual copies and tracking remain. The upstream index is not paginated: responses are capped at 32 MiB, scans at 2,000 series / 100,000 owned copies, and each series at 10,000 copies. Exceeding a bound fails the scan rather than silently truncating it.

## WeTrakr files

Choose WeTrakr in Imports and upload CSV or ZIP. The parser accepts explicit movie/show identifiers, historical plays, 0–10 ratings, notes, statuses and named lists. Lists are private when created; matching existing private manual lists receive additional memberships. Import requires both `library:write` and `lists:write`. Missing files never delete existing data. Dates and deterministic play identities keep partial-file retries additive.

Supported headers are case-insensitive with spaces/hyphens normalized to underscores:

| Data | Supported columns |
| --- | --- |
| Media | `type`, `media_type`, `kind`; movie / show / tv / series / season / episode |
| Identity | `tmdb_id`, `tmdb`, `imdb_id`, `imdb`; show rows can use `show_tmdb_id`, `series_tmdb_id`, `show_imdb_id` |
| Title | `show_title`, `series_title`, `title`, `name`, `year` |
| Playback | `watched_at`, `watched_date`, `finished_at`, `completed_at`; `season`/`season_number`, `episode`/`episode_number`; optional `history_id`/`play_id` |
| Rating | `rating`, `score`; `rated_at`, `scored_at`, `rating_date` |
| Organization | `status`, `watch_status`; `notes`, `review`, `comment`; `list_name`, `list` or CSV filename under `lists/` |

Episode rows must identify their parent show, not use an episode's TMDB ID as a show ID. Malformed dates and unrecognized row types fail visibly. The public WeTrakr site does not publish a stable export schema; the supported column inventory above is tested with synthetic fixtures, not certified against a live account export. Unsupported export variations require an explicit mapping update.

## Navidrome

Enable ListenBrainz in Navidrome and point `ND_LISTENBRAINZ_BASEURL` to `https://your-pastime.example/apis/listenbrainz/1/`. Create a sync token through the setup section and enter it in the Navidrome profile's ListenBrainz settings. The token expires after one year and can be revoked under Account → API tokens.

Submissions accept canonical `tracknumber` and legacy `track_number`, encoded as a positive integer or decimal string. Conflicting/invalid positions are rejected. Matching tries the recording ID, an unambiguous title, then a compatible track number. Repeated listens remain idempotent; now-playing notices do not create listens. Full submission content identifies queued batches, preventing unrelated batches with the same first listen from colliding.

## OpenCritic and connection health

`OPENCRITIC_API_KEY` is an optional RapidAPI key. Game pages return cached score, tier, recommendation percentage and refresh status. No OpenCritic call runs on an interactive request. A worker searches for an exact normalized title, rejects ambiguous matches, and checks the release year when supplied by the search result.

PostgreSQL shares a conservative **100-request calendar-month budget** across workers, including failed requests. Search and detail each consume a request. Successful/no-match results wait seven days before refresh; outages wait an hour; quota/credential failures wait a day. HTTP 429 also records shared `Retry-After` backoff. Stale scores remain available with their current health state. Subscription terms and availability must be checked before activation; no live subscription was used for verification.

Linked-service health distinguishes unknown, healthy, rejected credentials, unavailable service, rate limit, missing configuration and invalid responses. Observations are sanitized and fenced to the current connection generation. Transient failures use existing durable job retry behavior; they are not presented as invalid passwords.

## Background work

Statistics warming visits at most 25 active accounts per scheduler pass, committing the cursor and queued jobs together. Workers warm the default one-year window and its comparison window with a ten-second deadline and active-account checks. Warming is disabled without Valkey configuration. Existing PostgreSQL daily rollups remain authoritative; cache misses/outages recompute from durable data. Cache keys follow account change-feed and preference versions.

The reserved foreground worker lane handles scrobbles, ListenBrainz submissions and probes; bulk imports, critic refresh and statistics warming use the other lanes when concurrency is greater than one. UI mutations invalidate affected private projections, including episode ratings and status history. Shared media cards defer offscreen artwork with browser lazy loading; a bounded five-minute failure cache prevents repeated requests for failed cover URLs across remounts. Successful identical URLs use the browser's HTTP cache. There is no new server artwork proxy.

## Protocol evidence

- [Mylar3 API documentation](https://github.com/mylar3/mylar3/wiki/API-Documentation), plus response-field inspection at revision [`cdc94a44425b6f3f9cdb9b6e5f9d88ae2fee3316`](https://github.com/mylar3/mylar3/blob/cdc94a44425b6f3f9cdb9b6e5f9d88ae2fee3316/mylar/api.py): `success`/`data`, index IDs, issues/annuals and credential error semantics. No implementation was copied.
- [ListenBrainz submission metadata](https://github.com/metabrainz/listenbrainz-server/blob/master/docs/users/json.rst) and [Navidrome configuration](https://www.navidrome.org/docs/usage/configuration/options/), accessed 2026-09-27.
- [OpenCritic's RapidAPI listing](https://rapidapi.com/opencritic-opencritic-default/api/opencritic-api): `/game/search` and `/game/{id}`, accessed 2026-09-27.
- [WeTrakr product documentation](https://wetrakr.com/), accessed 2026-09-27; export-column limitation recorded above.

No dependency was added; existing exact-version dependency notices and licenses are unchanged. Tests use synthetic fixtures; none of these integrations has been checked against a live service.
