# October capabilities

Implemented independently from the behavior described in Floppy's public release notes v26.9.24, v26.10.1 and v26.10.8. This is a native implementation, not source parity: no Floppy or Silo code, tests, assets or history were imported.

## Ratings

- **Ratings off:** Settings → Display → Ratings → Off hides rating inputs, columns, sorts, the top-rated statistic and episode ratings. Saved ratings are kept and return when a scale is chosen again.
- **Unwatched episodes:** any episode of a tracked show can be rated without a play. The derived episode average counts every rated episode once; the watched count is reported separately.

## Lists and Home

- **Tier boards:** a manual list can be viewed as tiers (`GET/PUT /api/v1/lists/{listID}/tiers`). A board has up to 20 labelled, coloured tiers; members outside every tier are unranked. Arranging a board never changes the list's own order. "Fill from ratings" builds S/A/B/C/D tiers in the browser from 0–10 ratings (9–10, 7–8, 5–6, 3–4, 0–2). "Export image" draws the board to a PNG in the browser with titles as text, so it never depends on third-party artwork allowing canvas use.
- **Mixed Home rows:** a Home row can show library titles across several media types, optionally filtered by status, in a chosen order, with up to 30 titles.
- **Poster details:** for each media type, choose which of type, year, status, progress and rating appear under Home posters.

## Calendar

Settings → Display → Release dates for (a two-letter country) and Movie releases in the calendar (in cinemas, digital, disc) replace a movie's single primary date with that country's dates. A movie without dates for the country keeps its primary date. Dates come from TMDB (`release_dates`) when a movie is fetched or refreshed; the calendar feed gives each regional release its own event.

## Catalog providers

| Provider | Media type | Configuration | Notes |
| --- | --- | --- | --- |
| MangaBaka | manga | none | Alternative to AniList, which stays the default. Uses `api.mangabaka.org`; merged series are followed to their replacement. Records carry AniList, MyAnimeList and Kitsu ids. |
| Grand Comics Database | comic | optional `GCD_USERNAME`, `GCD_PASSWORD` | Answers anonymously; credentials are sent when set. Series carry their issue count; issues are not listed individually. Default for comics when Comic Vine is not configured. |
| TheTVDB | series, anime | `TVDB_API_KEY`, optional `TVDB_PIN` | Licensed key required. Seasons and episodes arrive with the series in the default order. |
| YouTube | video | none | Keyless oEmbed: a search names one video by link or id. Keyword search would need a YouTube Data API key. |

Search accepts an optional `provider`; `GET /api/v1/catalog/providers` lists each provider's media types and defaults. Discover and "Correct the match" offer a source picker when a media type has more than one provider.

## Moving between providers

"Correct the match" can search another provider, for example to move a show from TMDB to TheTVDB. Before matching, the new record's episodes are loaded for every season the entry has history, ratings or skips in. The preview now counts episode ratings that move or are dropped and lists up to 50 tracked or rated episodes the new record lacks. Episode ratings follow their episodes on remap; the title's own rating, notes and status always stay.

## Videos

`video` is a media type tracked like a movie: a play completes it. Search with a YouTube link or id; the record carries title, channel and thumbnail. Generic CSV imports accept `video` and `youtube` types.

## Reading and comic servers

Under Integrations → Linked services:

- **Komga** (server address and API key) syncs started and finished books read-only. Books with an ISBN, and EPUB or PDF books, are books; image-based books are grouped by series as comics with progress counted in finished issues.
- **Kavita** (server address and auth key) signs in through `/api/Plugin/authenticate` and syncs series with reading progress read-only. EPUB/PDF series are books; series with AniList or MyAnimeList ids are manga; series with a Comic Vine volume are comics.
- **Kapowarr** (server address and API key) records every issue with a file as an owned digital copy. Like Mylar3, a complete successful sync removes Kapowarr copies that are gone, and unlinking removes them all.

All three use the private-network policy for linked services and are tested against synthetic servers built from each project's published API.

## Device sign-in

TV and phone apps sign in without a keyboard: `POST /api/v1/device/authorize` returns a device code, an eight-letter user code and `verification_uri` (`/device`). A signed-in person approves the code there; the app polls `POST /api/v1/device/token` every `interval` seconds and receives an ordinary scoped API token once, valid for a year and revocable under Account → Tokens.

- Codes are stored only as SHA-256 digests and expire after ten minutes.
- Starting is limited to 10 a minute and polling to 60 a minute per address; code entry is limited to 10 a minute and 30 an hour per account.
- Polling faster than `interval` returns `slow_down` with a longer interval.
- An approval is bound to the account's authentication epoch: signing out everywhere, a password change or deactivation before pickup voids it.

## Already present

A recommendations API for external clients (`GET /api/v1/discover` with a `library:read` token), collecting a title by provider id (`POST /api/v1/catalog/resolve` then `/api/v1/library/track`) and Hardcover through its official API predate this batch. Listing by provider id is new: `GET /api/v1/library?provider=tmdb&provider_id=603` matches the catalog record or an external id such as `imdb`.
