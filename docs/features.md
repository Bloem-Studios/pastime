# Features

This page describes what Pastime does. Items that need a key, a server address or an account at another service say so. Settings for the server itself are in [configuration](configuration.md). The API is described by the OpenAPI document at `/api/openapi.json`, with a Swagger UI at `/docs/` (both need an administrator session).

## Media types and statuses

A library entry has one of twelve media types: movie, series, anime, book, manga, comic, game, board game, podcast, music, audiobook and video. Its status is planned, in progress, completed, paused, dropped or rated (rated without being tracked). Entries come from a catalog provider, or are added by hand as private manual entries that are never shared.

Automatic imports and scrobbles keep your choices: they do not overwrite a paused, dropped or rated-only status, and an automatic reprojection keeps a title you completed yourself. Automatic completion of a show or anime needs a positive terminal production status. Deleting a title records a marker, so later bulk syncs and request planning cannot bring it back. Adding it again clears the marker, and only a newer completed scrobble can recreate it.

## Catalog providers

Search and Discover use the provider for each media type. Providers that need a key are off until it is set, and a media type without a working provider searches the local catalog instead. When a media type has more than one provider, search takes a `provider` parameter, `GET /api/v1/catalog/providers` lists them, and Discover and "Correct the match" show a source picker.

| Provider | Media types | Configuration |
| --- | --- | --- |
| TMDB | movie, series | `TMDB_API_KEY` |
| TheTVDB | series, anime | `TVDB_API_KEY`, and `TVDB_PIN` for subscriber keys |
| AniList | anime, manga | none |
| MangaBaka | manga | none. An alternative to AniList. Merged series follow their replacement, and records carry AniList, MyAnimeList and Kitsu IDs |
| Open Library | book, audiobook | none |
| Audnexus | audiobook | none |
| IGDB | game | `IGDB_CLIENT_ID` and `IGDB_CLIENT_SECRET` |
| BoardGameGeek | board game | `BGG_TOKEN` |
| Comic Vine | comic | `COMICVINE_API_KEY` |
| Grand Comics Database | comic | none. `GCD_USERNAME` and `GCD_PASSWORD` are sent when set. It is the default for comics when Comic Vine is not configured. Series carry their issue count, and issues are not listed one by one |
| MusicBrainz | music | none |
| iTunes | podcast | none |
| RSS | podcast | none. A feed address is accepted only when it appears in an Apple podcast-directory record, so search the public podcast by name first. Private feeds are not supported |
| YouTube | video | none. Keyless oEmbed: search with a video link or ID |

`METADATA_LANGUAGE` sets the provider language and `PROVIDER_CONTACT` adds a contact to the User-Agent. Worker jobs refresh stale catalog records in the background.

Catalog pages include cast and crew pages (`/people/...`) and studio and network pages (`/studios/...`), editions of a book, alternate episode orders for a series, and for games a cached OpenCritic score (see below).

## Library and tracking

- **Library:** filters, sort, grid or list layout, facets, cursor pagination, and bulk actions on up to 200 titles (add or remove tags, set status, add to or remove from a list, delete).
- **Saved views:** a view stores filters, sort and layout. Views appear in the navigation and can be edited, reordered and deleted. An account has up to 100, and they stay private.
- **Progress:** episode, chapter, page and count progress, repeat cycles, restart, skipped episodes (a skipped episode counts toward completion but is not a play), and play history with plays you can add and delete.
- **Check-ins:** check in to a film, episode, issue or track. Films and episodes finish by themselves when their runtime ends. Books, comics, manga, audiobooks and games are sessions you finish yourself.
- **Ratings:** a scale of 10, 5 stars, 100, or off. Off hides rating inputs, columns, sorts and the top-rated statistic, and keeps saved ratings. `scored_at` records when a rating really changed, including clearing it. Imports and restores keep supplied historical dates.
- **Episode ratings:** any episode of a tracked show can be rated, watched or not. The derived series rating counts each rated episode once across repeat cycles, can be filtered by season and never overwrites your own series rating.
- **Status history:** each title shows its previous and new status with time and source. Only changes Pastime recorded appear.
- **Tags, owned copies and custom fields:** tags are private. Copies record physical or digital ownership with format, acquisition details and notes. Custom fields (text, number, date, yes or no, choice) appear on every copy. The Collection page searches copies by title, issue or edition and shows filtered totals for copies, titles and spending. Currencies stay separate.
- **Editions and episode order:** choose the edition you read (its page count becomes your total and its cover your artwork) or follow an alternate episode order.
- **Your own artwork:** replace the cover shown for a title. It is personal, and shared lists show the catalog artwork.
- **Correct the match:** point a title at another catalog record of the same kind, including another provider (for example TMDB to TheTVDB). A preview counts the episode ratings that move or are dropped and lists up to 50 tracked or rated episodes the new record lacks. Plays follow by season and episode number. Plays with no counterpart keep their original identity in history and out of progress.
- **Activity and history:** an activity timeline and a play history page.
- **Home:** configurable rows (continue, up next, coming soon, planned, recently finished, any list, or a mixed row of titles across media types filtered by status, ordered and limited to 30). You also choose which of type, year, status, progress and rating appear under posters for each media type.
- **Discover:** suggestions that are popular now, related to what you liked, or from your favourite genres. You can hide a suggestion and bring it back. Anime can be found as whole shows (TMDB) or season by season (AniList).
- **Recommendations between people:** recommend a title to another person on the same server, with a note. They accept or dismiss it, and they can decline new ones in their settings.

## Lists and sharing

- **Manual and smart lists.** A smart list selects titles by rules. Lists have a note per title and can be ordered.
- **Visibility:** private, unlisted or public. An unlisted list uses a random share link that expires after 90 days and can be renewed, which revokes the old link. A public profile (off by default) shows public lists at `/u/<username>`. Public lists are also available as JSON, RSS (`/lists/<username>/<slug>.rss`) and CSV with cursor pagination, and are never cached, so revoking a link takes effect on the next request. A frozen copy of a shared list becomes a new private manual list. Shared smart lists show that their rules can be inferred from their members.
- **Tier boards:** a manual list can be viewed as tiers (up to 20 labelled, coloured tiers; the rest are unranked) without changing the list's own order. "Fill from ratings" builds S to D tiers from your ratings in the browser, and "Export image" draws the board to a PNG in the browser.
- **Collaborative lists:** an owner invites other accounts on the server as editors (add, remove and order titles) or viewers (read only). Up to 100 owned lists and 50 people each.
- **Linked lists:** a manual list can follow a public Trakt or MDBList list. The first sync runs at once and the list refreshes daily. New titles are added to your library as planned, and existing tracking is kept. A list can also be copied to a private Trakt list when a Trakt account is linked.

## Calendar, notifications and statistics

- **Calendar:** release dates of planned, in-progress and paused titles, up to 400 days and 1,000 releases. A private subscription link (`/calendar/<secret>.ics`) works in calendar apps. It can be replaced or disabled, and password changes and access revocation remove it. Regional movie releases: set a country and the release types (in cinemas, digital, disc) in Settings to use that country's TMDB dates. A movie without dates for the country keeps its primary date.
- **Release notices:** a daily job announces releases a chosen number of days ahead, for the statuses and media types you choose. Each release is announced once, and a title can be muted. Destinations are webhook (signed with an HMAC), ntfy, Gotify, Discord and Apprise. Their addresses and tokens are encrypted and need `SECRET_KEY`. Destinations on private networks need `NOTIFY_ALLOW_PRIVATE=true`. Delivery is at least once.
- **Statistics:** plays, sessions, time, completed titles, streaks, time by media type, statuses, ratings, genres, people, studios, creators, time per day, week or month, most played and top rated, for windows up to ten years, with year-by-year comparison. Reading and game sessions count as time. Plays without a runtime are counted separately from summed minutes. Days are UTC.
- **Year in Review:** a page for any year since 1900 with statistics, a comparison with the year before, month by month totals, the biggest titles for each media type, the busiest day, the first play, new and returning titles, and the longest streak. The current year is reviewed so far.
- **Year in Review notices (optional):** with `REVIEW_NOTICES_URL` and `REVIEW_NOTICES_TOKEN` set, the worker sends each account's summary (year, minutes and number of titles) to a customer-notice service from 27 December. Pastime stores no email addresses. `pastime review-notices check [year]` does a dry run.
- **Critic scores:** with `OPENCRITIC_API_KEY` (a RapidAPI key), game pages show a cached OpenCritic score, tier and recommendation percentage. No OpenCritic call runs during a page request: a worker searches for the exact normalised title and rejects ambiguous matches. PostgreSQL shares a 100-request calendar-month budget across workers. Successful and no-match results are rechecked after seven days, outages after an hour, quota or credential failures after a day, and HTTP 429 honours `Retry-After`. Stale scores stay visible.

## Importing and exporting

Imports run as durable jobs, one at a time per account. They only add or upgrade: status never moves backwards, ratings and notes you have are kept, and every play carries its source identity, so running an import again adds nothing. Titles match by identifier first, then by exact title and year. Unmatched titles go to a review page where you can resolve or ignore them, and an import can be rolled back. Upload limit: 64 MiB, ZIP archives of at most 1,000 files, and 100,000 CSV rows. A limit fails the import and never truncates it silently.

| Source | Input |
| --- | --- |
| Trakt | data export ZIP |
| Letterboxd | export ZIP or CSV |
| IMDb | CSV |
| Goodreads, StoryGraph | CSV |
| AniList | a public username |
| Yamtrack, Grouvee, TV Time, CLZ | export files |
| Playnite | export file |
| WeTrakr | CSV or ZIP (see below) |
| Generic CSV | any CSV with the columns below, including `video` and `youtube` types |
| Pastime native | JSONL snapshot |

**Native export and restore.** `GET /api/v1/export/library.jsonl` writes a version 3 snapshot (restore also reads version 2) with entries, cycles, dates, edition choices, plays (including unmapped plays and runtime evidence), skips, copies, custom fields, tags, lists and notes, saved views, rating timestamps, episode ratings, status history, deletion markers, activity and resume positions. A manifest names the source and a SHA-256 trailer checks completeness. It needs `library:read` and `lists:read` and is limited to 64 MiB and 100,000 records. Restore needs `library:write` and `lists:write`, targets an empty library, keeps lists private, and applies once however often the file is retried. It restores library content, not credentials, collaborators, share links or sync settings.

**Other exports.** CSV, a Letterboxd-format CSV, scheduled snapshots kept on the server (every snapshot is compressed and kept up to a count you set) and snapshots made on demand. Ten exports per minute per account.

**WeTrakr files.** Lists are created private, and matching existing private manual lists receive the extra memberships. The import needs `library:write` and `lists:write`. Headers are case-insensitive, with spaces and hyphens read as underscores.

| Data | Supported columns |
| --- | --- |
| Media | `type`, `media_type`, `kind`; movie, show, tv, series, season, episode |
| Identity | `tmdb_id`, `tmdb`, `imdb_id`, `imdb`; show rows can use `show_tmdb_id`, `series_tmdb_id`, `show_imdb_id` |
| Title | `show_title`, `series_title`, `title`, `name`, `year` |
| Playback | `watched_at`, `watched_date`, `finished_at`, `completed_at`; `season` or `season_number`, `episode` or `episode_number`; optional `history_id` or `play_id` |
| Rating | `rating`, `score`; `rated_at`, `scored_at`, `rating_date` |
| Organisation | `status`, `watch_status`; `notes`, `review`, `comment`; `list_name`, `list`, or a CSV filename under `lists/` |

Episode rows name their parent show. Malformed dates and unknown row types fail visibly.

## Linked services

Under Integrations a person links services with a token, a username or a server address. A first sync runs at once, then on a schedule (hourly, every two hours, daily or weekly). Each sync is an ordinary import run, so it only adds or moves things forward. Credentials are encrypted with `SECRET_KEY`, so linked services need it. Servers on private networks need `SERVICES_ALLOW_PRIVATE=true` or an administrator. Each link reports its health: unknown, healthy, credentials rejected, service unavailable, rate limited, not configured or invalid response. Transient failures retry as durable jobs and are not shown as bad passwords.

| Service | What it brings in | Needs |
| --- | --- | --- |
| Trakt | history, ratings, watchlist | `TRAKT_CLIENT_ID`, `TRAKT_CLIENT_SECRET`; device sign-in |
| Simkl | watch lists and ratings | `SIMKL_CLIENT_ID`; PIN sign-in |
| MyAnimeList | anime and manga lists | `MAL_CLIENT_ID`, username |
| AniList, Kitsu | anime and manga lists | username |
| Hardcover | books | token |
| Steam | owned games | `STEAM_API_KEY`, Steam ID |
| PlayStation, Xbox | game libraries | token |
| BoardGameGeek | board game collection (owned, wishlist, ratings) and logged plays | `BGG_TOKEN`, username |
| Jellyfin, Emby | watched history | server address, token, username |
| Plex | watched history | server address, token, user |
| Audiobookshelf | listening progress | server address, token |
| Last.fm | listens | `LASTFM_API_KEY`, username |
| ListenBrainz, Koito | listens | token (Koito also a server address) |
| gPodder | subscriptions and finished episodes | server address, username, token |
| Pocket Casts | subscribed podcasts and finished episodes | username, token |
| Stremio | watched films and shows, the rest as planned | username, token |
| Storyteller | reading positions | server address, device sign-in |
| Radarr, Sonarr | your movies and shows, as planned titles | server address, API key |
| Komga | started and finished books and comics, read only | server address, API key |
| Kavita | series with reading progress, read only | server address, auth key |
| Mylar3 | owned comic issues | server address, API key |
| Kapowarr | owned comic issues | server address, API key |

A service shows as unavailable in the interface until its instance key is set where one is required.

**Jellyfin and Emby push.** A Jellyfin or Emby link can also push plays recorded in Pastime back to that server. Push is a separate durable job with per-play delivery receipts. Delivery is at least once.

**Komga, Kavita.** Komga books with an ISBN, and EPUB or PDF books, are books. Image-based books are grouped by series as comics, with progress counted in finished issues. Kavita EPUB and PDF series are books, series with AniList or MyAnimeList IDs are manga, and series with a Comic Vine volume are comics. Kavita signs in through `/api/Plugin/authenticate`.

**Mylar3 and Kapowarr.** Mylar3 needs Comic Vine configured to resolve series. Only Downloaded and Archived issues and annuals become digital owned copies, and ownership does not mean the comic was read. A scan is staged before it is applied, and only a fully successful run removes copies that Mylar3 no longer owns. Failures, cancellation and unmatched items never remove anything. Disconnecting removes that source's copies, and manual copies and tracking stay. Responses are capped at 32 MiB, scans at 2,000 series and 100,000 copies, and each series at 10,000. Exceeding a bound fails the scan. Kapowarr records every issue with a file as an owned copy and reconciles the same way.

**Media server webhooks.** `POST /webhooks/<kind>/<secret>` accepts completed viewings from Plex, Jellyfin, Emby, Kodi and Overseerr or Jellyseerr (`plex`, `jellyfin`, `emby`, `generic`, `seerr`). The `generic` kind takes a JSON body naming the movie or episode by TMDB, IMDb or TVDB ID. A webhook with a stable event ID uses permanent replay receipts. One without keeps a three-hour duplicate check.

## Music, reading and scrobbling

- **ListenBrainz-compatible endpoint:** Navidrome and other ListenBrainz clients can submit listens to `/apis/listenbrainz/1/`. Create a sync token in Pastime, enter it in the client, and point the client's ListenBrainz base URL at `https://your-pastime.example/apis/listenbrainz/1/`. The token lasts one year and can be revoked under Account. Submissions accept `tracknumber` and `track_number`. Matching tries the recording ID, then an unambiguous title, then a compatible track number. Repeated listens are idempotent and now-playing notices do not create listens.
- **KOReader sync:** a KOReader-compatible sync server at `/kosync/`. Set a separate sync password under Integrations, then sign in from KOReader. Positions merge by timestamp: older timestamps are rejected, equal timestamps with different positions conflict, and a newer timestamp can rewind.
- **Scrobble and history endpoints** for a media-server plugin: `POST /api/v1/scrobble/`, `GET /api/v1/history/` and `GET /api/v1/playback/progress/`.

## Accounts, profiles and sign-in

- **Roles:** owner, administrator and member. The first owner is created with `pastime bootstrap-owner`.
- **Admission:** invitations from an administrator, optional public registration (`PUBLIC_REGISTRATION=true`, always as a member), and OpenID Connect sign-in (`OIDC_*`), with optional automatic account creation. People can link or unlink a single sign-on identity.
- **Passwords and recovery:** a forgotten password is reset by an administrator through a one-time reset link, or by the person with an authenticator app (TOTP) and one-time recovery codes. Setting up authenticator recovery needs `SECRET_KEY`.
- **Sessions and tokens:** list and revoke sessions, sign out everywhere, and create personal API tokens (1 to 365 days, at most 20 active) with scopes `profile:read`, `library:read`, `library:write`, `lists:read` and `lists:write`. A token cannot administer accounts or create more credentials.
- **Device sign-in:** a TV or phone app starts `POST /api/v1/device/authorize`, shows the user code, and a signed-in person approves it at `/device`. The app polls `POST /api/v1/device/token` and receives a scoped token valid for a year. Codes are stored as SHA-256 digests, expire after ten minutes, and are rate limited. An approval is voided if the person signs out everywhere, changes their password or is deactivated before pickup.
- **Household profiles:** an account can hold several tracking profiles, each with its own library, history, statistics, lists and linked services. The web app has a profile chooser, and a request can name a profile with `X-Pastime-Profile-ID`. A profile can have its own login, set up by the household's primary user.
- **Rate limits** on login, registration, recovery, token creation and sync routes are kept in PostgreSQL. Per-address limits use real client addresses when `TRUSTED_PROXY_CIDRS` names your reverse proxy.

## Administration

A separate console (administrators only) with a dashboard of processes, database, counts, job queue and provider health, a jobs page (retry and cancel jobs), providers, catalog (refresh stale records), imports, users (create, change roles, deactivate, reset links, revoke access), invitations, an audit log, configuration checks that say what to change, and the API reference. Section search opens with Ctrl+K or Cmd+K, and a live queue indicator shows pending work. The interface language is English, German, French, Dutch or Spanish, and every string is translated in all of them.

## Background work

The worker (`pastime worker`) runs durable jobs from PostgreSQL: catalog refreshes, scrobbles, ListenBrainz submissions, imports, linked-service syncs, playback pushes, list syncs, release notices, scheduled exports, critic refreshes and statistics warming. It also finishes due check-ins every minute, prunes the change feed, sweeps expired data, and reports Silo sync keys that are about to expire. With two or more workers, one lane is reserved for scrobbles, ListenBrainz submissions and probes. Statistics warming needs Valkey. It visits at most 25 active accounts per pass and warms the default one-year window and its comparison window. Without Valkey, statistics are computed from the PostgreSQL daily rollups, which stay authoritative.

## Connecting to Bloem Server

The Bloem Pastime plugin links Bloem profiles to Pastime profiles:

- **Watch sync:** playback, finished plays, favourites, watchlist and ratings go from Bloem to Pastime, and plays, resume points, favourites, watchlist and ratings recorded elsewhere come back. See [Silo sync](integrations/silo.md).
- **Connect Silo page:** each person creates a sync key for a tracking profile and pastes it into Bloem. Keys last 90 days from their last renewal and renew while they are used.
- **Managed household tracking:** an administrator registers a Bloem Server with `pastime-bloem-register`, and Bloem can then provision accounts and profiles, with verified-identity sign-in. See [managed tracking](managed-tracking.md).
- **Bloem features shown in Pastime:** whether a title is available to watch in Bloem, a calendar of releases from Bloem, recommendations from Bloem with a hide and undo choice sent back, and browsing a connected Bloem library and queuing collection changes, an explicit import of an owned book copy from it, and Bloem reading a Pastime list you are allowed to see.

## MCP endpoint

`POST /mcp` lets an assistant use a library through a personal token (Model Context Protocol, streamable HTTP with JSON responses). The tools use the same rules and scopes as the REST API: `search_catalog`, `list_library`, `up_next`, `history`, `stats`, `year_in_review`, `add_to_library` and `record_play`.

## Interface

A responsive web app embedded in the server binary, with dark and light themes (or follow the system). Its browser tests include automated accessibility scans. It has a sidebar on large screens and a bottom bar on phones. See [interface design](design/README.md).
