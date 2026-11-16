# Silo sync through the plain Pastime plugin

This describes what the Bloem Pastime plugin (`bloem.pastime`,
[bloem-plugin-pastime](https://github.com/Bloem-Studios/bloem-plugin-pastime)) calls on Pastime, how
people connect profiles, and how sync keys behave. Bloem Server speaks Silo's watch-sync contract, which
is why Pastime's pages and routes say "Silo". The identifiers `bloem_*` (tables) and
`/api/v1/integrations/bloem/` (routes) keep their names.

## Model

Each Silo profile that syncs with Pastime is one watch-provider connection in Silo, holding one
sync key (`ptbs_…`). The key is bound to one Pastime binding: a registered Silo instance, a source
account and a source profile (`bloem_profiles`), which point at a Pastime tracking profile. The
plugin sends the binding's source IDs with every event; Pastime refuses events for another binding.

Bindings come from two places:

- **Provisioned** (`origin='provisioned'`): created through `profiles:provision` with the
  server's own account and profile IDs (the provisioning route).
- **Self service** (`origin='self_service'`): created on the Connect Silo page for a profile that
  has no binding yet, under the person's existing source account in that instance or a new one
  with source ID `pastime-account-<user id>`; the profile's source ID is
  `pastime-profile-<tracking profile id>`. Silo IDs are decimal (accounts) and UUIDs (profiles), so
  these cannot collide.

Snapshot completion and source account disable act only on provisioned bindings.

## Routes the plugin calls

All take `Authorization: Bearer ptbs_…` and JSON bodies with unknown fields refused.

| Route | Purpose |
| --- | --- |
| `POST …/credentials:inspect` | The key's binding: instance, source account and profile IDs, profile name |
| `POST …/events:apply` | One playback event (start, pause, stop; completed stops record a play once per consumption ID) |
| `POST …/state/events:apply` | One favourite, watchlist or rating change. `media.catalog_item_id` (optional) names a top-level catalog title when there are no external IDs, as for a removal keyed `catalog:<id>` |
| `POST …/state:read` | Pages of the profile's favourites, watchlist and ratings, and (families `watched`, `progress`) plays and resume points recorded outside Silo. The `watched` family also carries removals: a movie or show the profile deleted from its library while connected, as `removed: true` with the deletion time in `changed_at`, keyed `removed:catalog:<id>`. Silo then removes that profile's watches of the title up to that time and keeps later ones. A play of the title after the deletion retires the removal (table `watch_removals`) |

Answers the plugin relies on:

- `200` with `status` `applied`, `stale` or `no_change`. A completed stop whose consumption ID was
  already recorded answers `play_recorded: false`.
- `401 unauthenticated`: unknown, revoked or expired key. `403 service_access_required`: a valid key
  whose account has no tracking access under the service-access policy (only to callers that send `pevt_` event IDs). `403 forbidden`: the event names another binding.
- `404 no_match` / `not_found`: a definite refusal. `404 unknown_route`: Pastime has no such API
  route (a wrong address or version), never a refusal of the event. `409 conflict`: the event ID
  was used for a different payload, or the revision for different state. `409 rating_precision`: a
  Pastime rating has no exact Silo equivalent.
- `503 catalog_loading` (`Retry-After: 60`): the show's seasons are still loading; retry.
  `503 catalog_pending` (`Retry-After: 21600`): the episode lies just past what a running show lists
  (after the last listed episode of the latest season, up to 25 further, or early in the next
  season). A retry refetches the show and that season at most once an hour. Only events with a
  `pevt_` event ID get it; others get `404 no_match`. Neither
  `catalog_*` answer is logged as an error. Other `503` and `500` answers are service faults.
- `429 rate_limited` (`Retry-After: 60`).

## Rate limits

Each sync key has its own budget, `SILO_SYNC_REQUESTS_PER_MINUTE` (default 600), kept per key ID
after authentication. All sync traffic from one address has a ceiling of 6,000 requests a minute;
Silo syncs its connections one at a time and stays far below it. Keys Pastime never issued count per
address: after 120 in a minute, that address is refused with `429` before any key lookup until the
minute ends. Revoked or expired keys that Pastime did issue do not count, so a Silo server holding
one dead key keeps syncing its other connections. This relies on `TRUSTED_PROXY_CIDRS` behind a
reverse proxy, like every other per-address limit. Refused sync
requests are logged once a minute as counts per route and problem code, without keys or IDs.

## Key lifetime

A key is valid for 90 days from its last renewal. Each use of the current key renews it to 90 days
from now when fewer than 83 days remain, so a connection that keeps syncing never expires. Silo
stores no expiry or refresh token for these connections; a key that does expire answers `401`, and
the person creates a new one. Superseded keys are never renewed. Once a day the worker logs how many
current keys of active bindings expire within 30 days, with the earliest date, and how many already
expired while current (counts only). Such keys belong to connections that stopped syncing.

A key renews only when it authenticates, which needs tracking access. A profile without access keeps
its old expiry. Connect Silo keys need a signed-in person with access, so for a binding of an
account nobody can sign in to, the only way to a new key is machine reissue
(`profiles/{id}/credentials:issue` with the instance's `ptbp_` key). Keep that route and key until
every bound account can sign in.

## Connect Silo page

Session-only routes for the signed-in person:

- `GET /api/v1/integrations/silo/connect`: active tracking profiles, whether each has a current key
  and when it expires, the registered Silo address (when exactly one instance is active) and
  whether the person has tracking access.
- `POST /api/v1/integrations/silo/profiles/{id}/credential`: creates the profile's key and returns
  it once. A profile that already has a binding keeps it, so its source IDs and the consumption IDs
  Silo derives from them do not change. A binding or source account that the Connect Silo page
  created and that is inactive is reactivated; one that Silo provisioning turned off (a disabled
  Silo account, a removed Silo profile) stays off and the request answers
  `409 silo_account_inactive`. An excluded binding is refused with `409 profile_excluded`. With no registered Silo instance:
  `409 silo_not_registered`. A new key replaces the previous one as soon as Silo uses it, and after
  24 hours at the latest.
- `DELETE` on the same path revokes every key of the profile at once.

Creation and revocation need CSRF, are limited to 10 a minute per account, and are audited
(`silo.credential.create`, `silo.credential.revoke`). A Connect Silo key is stored only as its
digest (with a sealed empty value), so it cannot be revealed again; machine reissue answers `409`
for it. Registering the Silo instance itself still
uses `pastime-bloem-register`.

A Connect Silo key is the person's credential and ends with their sessions: password change or
recovery, reset-link claim, administrator revoke-access, a role or status change (reactivation
does not revive it) and claim withdrawal (`-revoke-claim`) revoke it, and the person creates a
new key. Without tracking access it answers `403` and works again when access returns. Keys
issued through machine reissue with the instance's `ptbp_` key stay under the provisioning-key
holder's control.

A person signed in with a profile login (Account → Profiles, set up by the household's primary
user) uses the same page for their own profile only. `GET …/connect` lists just that profile; creating
or revoking the key of any other profile answers `404`. Their requests count against their own rate
limit (keyed by the login name), not the primary user's. The primary user still sees and manages
every profile's key. Turning a profile login off, or resetting its password, ends that person's
browser sessions only: the profile's key keeps working until it is revoked here. No sync key,
binding or machine route can create, change or sign in with a profile login.

## Callers that send `bloem_evt_` event IDs

Callers that send `bloem_evt_` event IDs and the default Go User-Agent get these answers:
`404 no_match` instead of `503 catalog_pending`, and `401` instead of `403 service_access_required`.
`503 catalog_loading` reaches every caller. A revision below 10^12 that arrives after a stored one at
or above it counts as newer, so a caller that sends host sequence numbers instead of Unix
microseconds is not judged stale. A completion clears a resume point only when that point was not
updated more than five minutes after the completion. A route Pastime does not have answers
`404 unknown_route` instead of `404 not_found`.

## Upgrades and rollback

A Pastime binary refuses to start on a database schema newer than its own, so returning to an earlier
binary means restoring the backup taken before the upgrade. Back up before every upgrade. The plugin
needs a Pastime new enough for the routes it calls; upgrade Pastime before the plugin.
