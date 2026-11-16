#!/usr/bin/env python3
"""Authoritative native contract. Emit deterministic YAML without extra Python dependencies."""
import argparse
import re
import json
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
def ref(name): return {'$ref': '#/components/schemas/' + name}
def string(**kw):
    if kw.get('format') == 'uuid': kw['x-go-type'] = 'string'
    return {'type': 'string', **kw}
def integer(**kw): return {'type': 'integer', **kw}
def enum(*values): return string(enum=list(values))
def obj(properties, required=None):
    return {'type': 'object', 'additionalProperties': False, 'required': list(properties) if required is None else required, 'properties': properties}
def array(name): return {'type': 'array', 'items': ref(name)}
def items(name): return obj({'items': array(name)})
def paged(name): return obj({'items': array(name), 'next_cursor': string(nullable=True)})
ID = string(pattern='^[1-9][0-9]{0,18}$', description='Decimal identifier encoded as a string; never a JavaScript number.')
DATE = string(format='date-time')
FILE_SOURCES = ('trakt', 'letterboxd', 'imdb', 'goodreads', 'storygraph', 'anilist', 'native', 'yamtrack', 'grouvee', 'tvtime', 'csv', 'clz', 'wetrakr', 'playnite')
SERVICE_KINDS = ('trakt', 'simkl', 'anilist', 'mal', 'kitsu', 'hardcover', 'steam', 'bgg', 'jellyfin', 'emby', 'plex', 'audiobookshelf', 'lastfm', 'listenbrainz', 'koito', 'gpodder', 'pocketcasts', 'stremio', 'psn', 'xbox', 'storyteller', 'radarr', 'sonarr', 'mylar3', 'komga', 'kavita', 'kapowarr')
IMPORT_SOURCES = FILE_SOURCES + tuple(k for k in SERVICE_KINDS if k not in FILE_SOURCES)
VERSION = integer(minimum=1)
ROLE = enum('member', 'admin', 'owner')
TYPE = enum('movie', 'series', 'anime', 'book', 'manga', 'comic', 'game', 'boardgame', 'podcast', 'music', 'audiobook', 'video')
KIND = enum('movie', 'series', 'season', 'episode', 'anime', 'manga', 'book', 'audiobook', 'comic', 'comic_issue', 'game', 'boardgame', 'music', 'track', 'podcast', 'podcast_episode', 'video')
NDATE = {**string(format='date-time'), 'nullable': True}
NID = {**string(pattern='^[1-9][0-9]{0,18}$'), 'nullable': True}
NINT = integer(nullable=True)
STRINGS = {'type': 'array', 'items': string()}
LIST_VISIBILITY = enum('private', 'unlisted', 'public')
LIST_SORT = enum('position', 'added', 'title', 'rating', 'year', 'activity')
COPY_FORMAT = enum('digital', 'uhd_bluray', 'bluray', 'dvd', 'vhs', 'hardcover', 'paperback', 'ebook', 'audiobook', 'vinyl', 'cd', 'cassette', 'game_physical', 'game_digital', 'boxed', 'other')
FIELD_KIND = enum('text', 'number', 'date', 'boolean', 'choice')
STATUS = enum('planned', 'in_progress', 'completed', 'paused', 'dropped', 'rated')
UNIT = enum('minutes', 'episodes', 'pages', 'chapters', 'tracks', 'items')
PROGRESS = integer(minimum=0, maximum=100000000)
PASSWORD = string(minLength=12, maxLength=128, format='password', writeOnly=True)
USERNAME = string(minLength=3, maxLength=48, pattern='^[a-zA-Z0-9][a-zA-Z0-9_.-]{2,47}$')
SCOPES = {'type': 'array', 'minItems': 1, 'maxItems': 5, 'uniqueItems': True, 'items': enum('profile:read', 'library:read', 'library:write', 'lists:read', 'lists:write')}
SESSION = [{'SessionCookie': []}, {'SecureSessionCookie': []}]
TOKEN = SESSION + [{'BearerToken': []}]
entry = dict(title=string(minLength=1, maxLength=200), media_type=TYPE, year=integer(nullable=True, minimum=1, maximum=9999), unit=UNIT,
             total=integer(nullable=True, minimum=1, maximum=100000000), status=STATUS, progress=PROGRESS,
             rating=integer(nullable=True, minimum=0, maximum=10))
summary = dict(scored_at=NDATE, id=ID, **entry, cycle=integer(minimum=1), completed_cycles=integer(minimum=0), version=VERSION, created_at=DATE, updated_at=DATE,
               catalog_id=NID, image_url=string(), backdrop_url=string(description='Wide artwork from the catalog; empty when none.'), original_title=string(description='Original-language title from the catalog; empty when unknown or the same.'), started_at=NDATE, finished_at=NDATE, last_activity_at=NDATE,
               localized_title=string(nullable=True, description='The title in the language chosen in preferences, when the catalog knows it.'),
               last_played_at={**NDATE, 'description': 'The latest play that was a viewing; null when none. Bulk-marked plays do not count.'})
schemas = {
    'Liveness': obj({'status': enum('alive')}),
    'Readiness': obj({'status': enum('ready', 'degraded', 'unavailable'), 'database': enum('ready', 'unavailable'), 'valkey': enum('ready', 'unavailable', 'disabled')}),
    'ServerInfo': obj({'name': enum('pastime'), 'version': string(minLength=1), 'stage': enum('preview')}),
    'Problem': {**obj({'code': string(description='Stable machine-readable reason.'), 'message': string(description='Human-readable explanation.')}), 'example': {'code': 'conflict', 'message': 'This record changed or already exists. Refresh and try again.'}},
    'User': obj(dict(id=ID, username=string(), display_name=string(), email=string(), role=ROLE, active={'type': 'boolean'}, password_ready={'type': 'boolean'}, version=VERSION, created_at=DATE, last_login_at={**DATE, 'nullable': True})),
    'TrackingProfile': obj({'id':ID,'account_id':ID,'name':string(),'default':{'type':'boolean'},'bloem_available':{'type':'boolean'},'bloem_linked':{'type':'boolean'},'bloem_active':{'type':'boolean'},'archived_at':{**DATE,'nullable':True},'created_at':DATE},['id','account_id','name','default','created_at']),
    'TrackingProfilePage': items('TrackingProfile'),
    'SessionView': obj({'user': ref('User'), 'csrf_token': string(pattern='^[a-f0-9]{64}$'),
        'profile_login': {'allOf': [ref('ProfileLoginRef')], 'nullable': True, 'description': 'Set when this browser signed in with a profile login: the session acts only for that profile, and user shows the login name and profile name instead of the household account. Null for the account itself.'}}),
    'ProfileLoginRef': obj({'profile_id': ID, 'login_name': string()}),
    'ProfileLogin': obj({'profile_id': ID, 'login_name': string(), 'created_at': DATE, 'last_login_at': NDATE}),
    'ProfileLoginPage': items('ProfileLogin'),
    'ProfileLoginInput': obj({'login_name': string(minLength=3, maxLength=254, description='A username (3 to 48 letters, numbers, dots, underscores or hyphens) or an email address. Stored lowercase; never verified or mailed. Must not match any account username or other profile login.'),
        'password': {**PASSWORD, 'description': 'Required to turn a login on. Omit it to rename without signing the person out; any new value resets the password and ends every session of the login.'}}, ['login_name']),
    'RegistrationInput': obj({'username': USERNAME, 'display_name': string(maxLength=80), 'email': string(minLength=1,maxLength=254), 'password': PASSWORD}, ['username','email','password']),
    'LoginInput': obj({'username': string(maxLength=254, description='An account username, or a profile login name (which may be an email address).'), 'password': string(maxLength=128, format='password', writeOnly=True)}),
    'UserPatch': obj({'version': VERSION, 'display_name': string(minLength=1, maxLength=80), 'email': string(maxLength=254), 'role': ROLE, 'active': {'type': 'boolean'}}, ['version']),
    'PasswordInput': obj({'current_password': string(format='password', writeOnly=True, maxLength=128), 'password': PASSWORD}),
    'SessionRecord': obj({'id': string(format='uuid'), 'created_at': DATE, 'expires_at': DATE, 'current': {'type': 'boolean'},
        'user_agent': string(maxLength=512, description='The User-Agent the browser signed in with; empty for sessions from before it was recorded.'),
        'last_used_at': {**NDATE, 'description': 'When the session was last used, to within five minutes; null until an older session is used again.'}}),
    'SessionPage': items('SessionRecord'),
    'TokenRecord': obj({'id': string(format='uuid'), 'name': string(), 'scopes': SCOPES, 'created_at': DATE, 'expires_at': DATE, 'revoked': {'type': 'boolean'}, 'profile_id': {**ID, 'description': 'The tracking profile the token acts for.'}}),
    'TokenPage': items('TokenRecord'),
    'TokenInput': obj({'name': string(minLength=1, maxLength=80), 'scopes': SCOPES, 'expires_in_days': integer(minimum=1, maximum=365), 'password': string(format='password', writeOnly=True, maxLength=128)}),
    'TokenCreated': obj({'token': ref('TokenRecord'), 'secret': string(description='Shown once. Store securely; never put in a URL.')}),
    'CreateUserInput': obj({'username': USERNAME, 'display_name': string(maxLength=80), 'email': string(maxLength=254), 'role': ROLE}, ['username', 'role']),
    'InviteInput': obj({'role': ROLE, 'recipient_email': string(maxLength=254), 'expires_in_hours': integer(minimum=1, maximum=168, default=72)}, ['role']),
    'Invitation': obj({'id': string(format='uuid'), 'kind': enum('join', 'reset'), 'role': ROLE, 'recipient_email': string(), 'username': string(), 'status': enum('pending', 'claimed', 'revoked', 'expired'), 'created_at': DATE, 'expires_at': DATE}),
    'InvitationPage': items('Invitation'),
    'InviteCreated': obj({'invitation': ref('Invitation'), 'claim_url': string(format='uri', description='Secret appears only in the URL fragment; displayed once. Deliver privately. No email is sent.')}),
    'UserCreated': obj({'user': ref('User'), 'invitation': ref('Invitation'), 'claim_url': string(format='uri')}),
    'InviteTokenInput': obj({'token': string(minLength=48, maxLength=48, writeOnly=True)}),
    'InvitePreview': obj({'kind': enum('join', 'reset'), 'role': ROLE, 'username': string(), 'expires_at': DATE}),
    'ClaimInput': obj({'token': string(minLength=48, maxLength=48, writeOnly=True), 'username': USERNAME, 'display_name': string(maxLength=80), 'password': PASSWORD}, ['token', 'password']),
    'UserPage': paged('User'),
    'AuditEvent': obj({'id': ID, 'actor': string(), 'action': string(), 'target_kind': string(), 'target_id': string(), 'target_label': string(description='The target named for people: the current username of an account. Empty when the target has no name or no longer exists.'), 'created_at': DATE}),
    'AuditPage': paged('AuditEvent'),
    'EntrySummary': obj({**summary, 'list_note': string(description='Your note on this title within the list; present only when browsing a manual list.')}, list(summary)),
    'Entry': obj({**summary, 'notes': string(maxLength=10000), 'edition': {'allOf': [ref('Edition')], 'nullable': True, 'description': 'The edition you read, when chosen; its pages and cover replace the work\'s for you.'},
        'episode_order': string(nullable=True, description='The alternate episode order chosen for a show, when any.'),
        'next_item': {'allOf': [ref('NextItem')], 'nullable': True, 'description': 'A show\'s next episode: the first regular one, in its watch order, with no play or skip in the current cycle. It may not have aired yet. Null for other titles and when none is left.'}}),
    'NextItem': obj({'catalog_id': ID, 'season_number': NINT, 'episode_number': NINT, 'title': string(),
        'release_date': {**string(pattern='^[0-9]{4}-[0-9]{2}-[0-9]{2}$', description='Calendar date (YYYY-MM-DD).'), 'nullable': True}, 'runtime_minutes': NINT}),
    'Edition': obj({'id': string(), 'title': string(), 'publisher': string(), 'year': string(), 'pages': NINT, 'isbn': string(), 'format': string(), 'language': string(), 'image_url': string()}),
    'EditionPage': items('Edition'),
    'EpisodeOrderSummary': obj({'id': string(), 'name': string(), 'kind': string()}),
    'EpisodeOrderPage': items('EpisodeOrderSummary'),
    'EpisodeOrderGroup': obj({'number': integer(minimum=1), 'name': string(), 'episodes': array('CatalogItem')}),
    'EpisodeOrderDetail': obj({'id': string(), 'name': string(), 'kind': string(), 'groups': array('EpisodeOrderGroup')}),
    'EpisodeOrderChoice': obj({'version': integer(minimum=1), 'order_id': string(nullable=True, maxLength=100)}),
    'EditionChoice': obj({'version': integer(minimum=1), 'edition_id': string(nullable=True, maxLength=40)}),
    'EntryInput': obj({**entry, 'notes': string(maxLength=10000), 'version': VERSION}, ['title', 'media_type', 'unit']),
    'EntryPage': paged('EntrySummary'),
    'LibraryFacets': obj({'total': integer(minimum=0, description='Titles matching every filter.'),
        'media_types': {**array('MediaTypeCount'), 'description': 'Media types with titles under every filter except media_type, in a fixed order.'},
        'statuses': {**array('StatusCount'), 'description': 'Statuses with titles under every filter except status, in a fixed order.'}}),
    'MediaTypeCount': obj({'media_type': TYPE, 'count': integer(minimum=1)}),
    'StatusCount': obj({'status': STATUS, 'count': integer(minimum=1)}),
    'ProgressInput': obj({'version': VERSION, 'progress': PROGRESS, 'status': STATUS}),
    'VersionInput': obj({'version': VERSION}),
    'Mutation': obj({'id': ID, 'version': VERSION, 'replayed': {'type': 'boolean'}, 'deleted': {'type': 'boolean'}}),
    'Stats': obj({key: integer(minimum=0) for key in ['total', 'in_progress', 'planned', 'completed', 'completed_cycles']}),
    'ActivityEvent': obj({'id': ID, 'entry_id': ID, 'title': string(), 'media_type': TYPE, 'kind': enum('added', 'updated', 'progress', 'restarted', 'played', 'unplayed'), 'status': STATUS, 'progress': PROGRESS, 'unit': UNIT, 'cycle': integer(minimum=1), 'created_at': DATE,
        'image_url': string(description='The title\'s current cover; empty when none.'), 'localized_title': string(nullable=True, description='The title in the language chosen in preferences, when the catalog knows it.'),
        'import_id': {**string(format='uuid'), 'nullable': True, 'description': 'The import run that wrote this event; null for anything else.'}, 'import_source': {**enum(*IMPORT_SOURCES), 'nullable': True, 'description': 'The source of that import run.'}}),
    'ActivityPage': paged('ActivityEvent'),
    'Rules': obj({'query': string(maxLength=100), 'media_types': {'type': 'array', 'items': TYPE, 'maxItems': 11}, 'statuses': {'type': 'array', 'items': STATUS, 'maxItems': 6},
                  'genres': {'type': 'array', 'items': string(maxLength=60), 'maxItems': 20}, 'tag_ids': {'type': 'array', 'items': ID, 'maxItems': 20},
                  'min_rating': integer(minimum=0, maximum=10), 'year_from': integer(minimum=1, maximum=9999), 'year_to': integer(minimum=1, maximum=9999), 'owned': {'type': 'boolean'}}, []),
    'List': obj({'share_token': string(nullable=True), 'share_expires_at': NDATE, 'id': ID, 'name': string(), 'description': string(), 'visibility': LIST_VISIBILITY, 'slug': string(nullable=True), 'kind': enum('manual', 'smart'), 'rules': ref('Rules'),
                 'sort': LIST_SORT, 'version': VERSION, 'item_count': integer(minimum=-1, description='Members of a manual list; -1 for a smart list, whose members are computed.'), 'created_at': DATE, 'updated_at': DATE,
                 'source': {'allOf': [ref('ListSource')], 'nullable': True, 'description': 'The Trakt or MDBList list this one mirrors; never shown publicly.'}}),
    'ListSource': obj({'kind': enum('trakt', 'mdblist'), 'url': string(), 'synced_at': NDATE}),
    'ListSourceInput': obj({'url': string(maxLength=500)}),
    'TraktExport': obj({'added': integer(minimum=0), 'existing': integer(minimum=0), 'skipped': integer(minimum=0), 'url': string()}),
    'ListInput': obj({'snapshot_of': string(nullable=True, description='On creation of a private manual list, freeze the current members of this owned list.'), 'rotate_share': {'type': 'boolean', 'description': 'Issue a new unlisted link valid for 90 days; revokes the previous link.'}, 'name': string(minLength=1, maxLength=100), 'description': string(maxLength=2000), 'visibility': LIST_VISIBILITY, 'slug': string(nullable=True, pattern='^[a-z0-9]([a-z0-9-]{0,58}[a-z0-9])?$'),
                      'kind': enum('manual', 'smart'), 'rules': ref('Rules'), 'sort': LIST_SORT, 'version': VERSION}, ['name']),
    'ListPage': paged('List'),
    'ListItems': items('List'),
    'MembershipInput': obj({'version': VERSION, 'present': {'type': 'boolean'}}),
    'CatalogItem': obj({'id': ID, 'kind': KIND, 'provider': string(), 'provider_id': string(), 'parent_id': NID, 'season_number': NINT, 'episode_number': NINT,
        'title': string(), 'original_title': string(), 'overview': string(), 'image_url': string(), 'backdrop_url': string(),
        'release_date': {**string(pattern='^[0-9]{4}-[0-9]{2}-[0-9]{2}$', description='Calendar date (YYYY-MM-DD).'), 'nullable': True}, 'year': NINT, 'runtime_minutes': NINT, 'unit': UNIT, 'total': NINT,
        'release_status': string(), 'genres': STRINGS, 'credits': STRINGS, 'external_ids': {'type': 'object', 'additionalProperties': True},
        'details': {'type': 'object', 'additionalProperties': True}, 'provider_rating': {'type': 'number', 'nullable': True},
        'children_loaded': {'type': 'boolean'}, 'refreshed_at': DATE}),
    'CatalogItemPage': items('CatalogItem'),
    'CatalogDetail': obj({'item': ref('CatalogItem'), 'entry': {'allOf': [ref('Entry')], 'nullable': True}}),
    'CatalogResult': obj({'provider': string(), 'provider_id': string(), 'media_type': KIND, 'title': string(), 'year': NINT, 'image_url': string(),
        'overview': string(), 'credits': STRINGS, 'catalog_id': NID, 'library_entry_id': NID, 'library_status': {**STATUS, 'nullable': True}}),
    'CatalogSearch': obj({'items': array('CatalogResult'), 'source': enum('provider', 'local'), 'degraded': {'type': 'boolean'}, 'provider': string(), 'availability': enum('available', 'local_only', 'not_configured', 'temporarily_unavailable', 'search_only'), 'next_page': {**string(pattern='^[0-9]{1,3}$'), 'nullable': True}}),
    'ResolveInput': obj({'provider': string(maxLength=32), 'media_type': KIND, 'provider_id': string(minLength=1, maxLength=200)}),
    'TrackInput': obj({'catalog_id': ID, 'status': STATUS, 'rating': integer(nullable=True, minimum=0, maximum=10)}, ['catalog_id']),
    'Play': obj({'unmapped': {'type': 'boolean'}, 'id': ID, 'catalog_id': ID, 'cycle': integer(minimum=1), 'played_at': DATE, 'source': string()}),
    'PlayPage': items('Play'),
    'PlayItem': obj({'catalog_id': ID, 'played_at': {**DATE, 'nullable': True}}, ['catalog_id']),
    'HistoryItem': obj({'unmapped': {'type': 'boolean'}, 'play_id': ID, 'played_at': DATE, 'cycle': integer(minimum=1), 'source': string(), 'entry_id': ID, 'entry_title': string(), 'media_type': TYPE,
        'image_url': string(), 'catalog_id': ID, 'item_title': string(), 'season_number': NINT, 'episode_number': NINT, 'minutes': NINT,
        'marked': {'type': 'boolean', 'description': 'A bulk "mark as played": three or more items recorded in the same minute. It counts toward progress, not toward time, plays or streaks.'}}),
    'HistoryPage': paged('HistoryItem'),
    'Count': obj({'name': string(), 'count': integer(minimum=0)}),
    'TypeStats': obj({'unknown_runtime': integer(minimum=0,description='Plays excluded from timed totals because no runtime is known.'), 'media_type': TYPE, 'entries': integer(minimum=0), 'completed': integer(minimum=0), 'plays': integer(minimum=0), 'sessions': integer(minimum=0,description='Finished reading and game sessions that recorded no play; their time is in minutes.'), 'minutes': integer(minimum=0)}),
    'DayStats': obj({'unknown_runtime': integer(minimum=0,description='Plays excluded from timed totals because no runtime is known.'), 'date': string(pattern='^[0-9]{4}-[0-9]{2}-[0-9]{2}$'), 'plays': integer(minimum=0), 'sessions': integer(minimum=0,description='Finished reading and game sessions that recorded no play; their time is in minutes.'), 'minutes': integer(minimum=0)}),
    'RankedEntry': obj({**summary, 'plays': integer(minimum=0)}),
    'Insights': obj({'unknown_runtime': integer(minimum=0,description='Plays excluded from timed totals because no runtime is known.'), 'from': DATE, 'to': DATE, 'plays': integer(minimum=0), 'sessions': integer(minimum=0,description='Finished reading and game sessions that recorded no play; their time is in minutes.'), 'minutes': integer(minimum=0), 'completed': integer(minimum=0),
        'streaks': ref('Streaks'), 'by_type': array('TypeStats'), 'statuses': array('Count'), 'ratings': array('Count'), 'genres': array('Count'), 'studios': array('Count'), 'people': array('Count'),
        'creators': {**array('Creator'), 'description': 'Up to five per media type: authors, developers, artists, cast and crew.'},
        'days': array('DayStats'), 'top_rated': array('EntrySummary'), 'most_played': array('RankedEntry')}),
    'Streak': obj({'days': integer(minimum=0), 'from': string(pattern='^[0-9]{4}-[0-9]{2}-[0-9]{2}$'), 'to': string(pattern='^[0-9]{4}-[0-9]{2}-[0-9]{2}$')}, ['days']),
    'Streaks': obj({'current': ref('Streak'), 'longest': ref('Streak')}),
    'Creator': obj({'media_type': TYPE, 'name': string(), 'count': integer(minimum=1)}),
    'YearType': obj({'media_type': TYPE, 'plays': integer(minimum=0), 'sessions': integer(minimum=0), 'minutes': integer(minimum=0)}),
    'YearStats': obj({'year': integer(minimum=1900), 'plays': integer(minimum=0), 'sessions': integer(minimum=0), 'minutes': integer(minimum=0), 'active_days': integer(minimum=0), 'completed': integer(minimum=0, description='Titles whose latest completion falls in the year.'), 'by_type': array('YearType')}),
    'YearStatsPage': items('YearStats'),
    'ReviewTitle': obj({**summary, 'plays': integer(minimum=0), 'minutes': integer(minimum=0)}),
    'ReviewType': obj({'media_type': TYPE, 'plays': integer(minimum=0), 'sessions': integer(minimum=0), 'minutes': integer(minimum=0), 'titles': {**array('ReviewTitle'), 'maxItems': 3, 'description': 'The three titles with the most time, then plays.'}}),
    'MonthStats': obj({'month': integer(minimum=1, maximum=12), 'plays': integer(minimum=0), 'sessions': integer(minimum=0), 'minutes': integer(minimum=0), 'active_days': integer(minimum=0)}),
    'YearReview': obj({'year': integer(minimum=1900), 'complete': {'type': 'boolean', 'description': 'False while the year is still running.'}, 'stats': ref('Insights'),
        'previous': {'allOf': [ref('YearStats')], 'nullable': True, 'description': 'The year before, for comparison; null without activity.'},
        'months': {**array('MonthStats'), 'minItems': 12, 'maxItems': 12}, 'types': array('ReviewType'),
        'busiest_day': {'allOf': [ref('DayStats')], 'nullable': True}, 'first_play': {'allOf': [ref('HistoryItem')], 'nullable': True},
        'new_titles': integer(minimum=0, description='Titles first played or read this year.'), 'returning': integer(minimum=0, description='Titles with plays or sessions before this year.')}),
    'Release': obj({'date': string(pattern='^[0-9]{4}-[0-9]{2}-[0-9]{2}$'), 'catalog_id': ID, 'kind': KIND, 'title': string(), 'season_number': NINT, 'episode_number': NINT, 'release_type': {**enum('theatrical', 'digital', 'physical'), 'description': 'A movie regional release date; absent for a primary release date.'},
        'entry_id': ID, 'entry_title': string(), 'media_type': TYPE, 'image_url': string(), 'backdrop_url': string(description='Wide artwork for the root title; absent when unavailable.'), 'title_catalog_id': ID},
        ['date', 'catalog_id', 'kind', 'title', 'season_number', 'episode_number', 'entry_id', 'entry_title', 'media_type', 'image_url', 'title_catalog_id']),
    'ReleasePage': items('Release'),
    'UpNext': obj({'entry': ref('EntrySummary'), 'episode': ref('Release')}),
    'Home': obj({'continue': array('EntrySummary'), 'up_next': array('UpNext'), 'upcoming': array('CalendarRelease'), 'planned': array('EntrySummary'),
        'recently_finished': array('EntrySummary'), 'stats': ref('Stats')}),
    'FeedStatus': obj({'enabled': {'type': 'boolean'}, 'created_at': NDATE}),
    'FeedCreated': obj({'url': string(format='uri', description='Contains the feed secret. Shown once; treat it like a password.')}),
    'ConnectionSettings': obj({'username': string(maxLength=100, description='Only accept events from this media server account; empty accepts all accounts on the server.')}),
    'Connection': obj({'id': string(format='uuid'), 'kind': enum('plex', 'jellyfin', 'emby', 'generic', 'seerr'), 'settings': ref('ConnectionSettings'), 'created_at': DATE,
        'last_event_at': NDATE, 'last_outcome': string(), 'events_recorded': integer(minimum=0)}),
    'ConnectionPage': items('Connection'),
    'ConnectInput': obj({'kind': enum('plex', 'jellyfin', 'emby', 'generic', 'seerr'), 'settings': ref('ConnectionSettings')}, ['kind']),
    'ConnectionUpdate': obj({'settings': ref('ConnectionSettings')}),
    'ConnectionCreated': obj({'connection': ref('Connection'), 'webhook_url': string(format='uri', description='Contains the webhook secret. Shown once; paste it into the media server.')}),
    'WebhookResult': obj({'status': enum('queued', 'ignored'), 'reason': string()}),
    'ImportRun': obj({'id': string(format='uuid'), 'source': enum(*IMPORT_SOURCES),
        'status': enum('queued', 'running', 'succeeded', 'partial_success', 'failed', 'cancelled', 'rolled_back'), 'cancel_requested': {'type': 'boolean'}, 'upload_name': string(),
        'total': integer(minimum=0), 'processed': integer(minimum=0), 'imported': integer(minimum=0), 'plays_added': integer(minimum=0),
        'unchanged': integer(minimum=0), 'unmatched': integer(minimum=0), 'failed': integer(minimum=0),
        'unmatched_sample': {'type': 'array', 'maxItems': 200, 'items': string()}, 'error': string(), 'created_at': DATE, 'started_at': NDATE, 'finished_at': NDATE}),
    'ImportPage': items('ImportRun'),
    'PlayInput': obj({'version': VERSION, 'items': {'type': 'array', 'minItems': 1, 'maxItems': 500, 'items': ref('PlayItem')}}),
}
# Backend-only native provisioning. No ordinary session/token grants enrollment.
BLOEM_PROVISION=[{'BloemProvisionCredential': []}]
OPAQUE=string(minLength=1,maxLength=128)
REVISION=integer(minimum=1,maximum=9223372036854775807)
schemas.update({
 'BloemProvisionInput':obj({'version':integer(enum=[1]),'instance_id':OPAQUE,'account_id':OPAQUE,'account_name':string(minLength=1,maxLength=80),'email':string(maxLength=254),'profile_id':OPAQUE,'default_profile_id':OPAQUE,'name':string(minLength=1,maxLength=80),'account_revision':REVISION,'revision':REVISION,'account_active':{'type':'boolean'},'active':{'type':'boolean'},'identity_issuer':string(maxLength=500),'identity_subject':string(maxLength=255)},['version','instance_id','account_id','account_name','profile_id','default_profile_id','name','account_revision','revision','account_active','active']),
 'BloemProvisionResult':obj({'account_id':ID,'profile_id':ID,'link_status':enum('pending','linked'),'connection_ready':{'type':'boolean','description':'True only once backend connection enrollment is durably confirmed; mapping alone is insufficient.'}}),
 'BloemCredentialInput':obj({'issuance_id':OPAQUE}),
 'BloemCredentialResult':obj({'credential':string(format='password',description='Backend-only profile sync credential. Never persist in browser storage or display in the application UI.'),'expires_at':DATE}),
})
calendar_release = {**schemas['Release']['properties'], 'kind': enum(*KIND['enum'], 'ebook'),
 'source_instance_id': string(), 'source_event_id': string(), 'source_content_id': string(),
 'source_authority_expires_at': DATE, 'air_at': DATE, 'air_timezone': string(), 'handoff': {'allOf': [ref('BloemHandoffTarget')], 'nullable': True},
 **{key: {'type': 'boolean'} for key in ('watched', 'followed', 'favourite', 'watchlist', 'playable')}}
schemas['CalendarRelease'] = obj(calendar_release, [key for key in schemas['Release']['required'] if key not in ('catalog_id', 'entry_id', 'title_catalog_id')] + ['watched', 'followed', 'favourite', 'watchlist', 'playable', 'handoff'])
schemas['PersonalMemberships']=obj({'favourite':{'type':'boolean'},'watchlist':{'type':'boolean'}})
schemas['PersonalMembershipInput']=obj({'kind':enum('favorite','watchlist'),'present':{'type':'boolean'}})
schemas['CalendarReleasePage'] = items('CalendarRelease')
CALENDAR_FILTER = {'name': 'filter', 'in': 'query', 'required': False, 'schema': {**enum('all', 'following', 'favourites', 'watchlist'), 'default': 'all'}}

schemas['BloemHandoffInstance'] = obj({'id': string(), 'url': string(), 'active': {'type': 'boolean'}})
schemas['BloemHandoffInstancePage'] = items('BloemHandoffInstance')
schemas['BloemHandoffTarget'] = obj({'url': string(), 'label': string()})
schemas['BloemHandoffSettings'] = obj({'enabled': {'type': 'boolean'}, 'label': string(minLength=1,maxLength=100), 'base_url': string(maxLength=500)})
paths = {}
def parameter(name, schema, location='query', required=False, description=None):
    v = {'name': name, 'in': location, 'required': required, 'schema': schema}
    if description: v['description'] = description
    return v
PAGINATION = [parameter('cursor', string(maxLength=8192), description='Opaque list cursor bound to the filters; not a synchronization checkpoint. Refresh to see new commits.'), parameter('limit', integer(minimum=1, maximum=100, default=30))]
ORIGIN = parameter('Origin', string(), 'header', True, 'Must exactly equal the configured PUBLIC_ORIGIN. Browsers set this header automatically.')
CSRF = parameter('X-CSRF-Token', string(pattern='^[a-f0-9]{64}$'), 'header', False, 'Required with session cookies on mutations. Obtain from GET /api/v1/session. Also send the exact configured Origin. Not needed with a bearer token.')
IDEMPOTENCY = parameter('Idempotency-Key', string(format='uuid'), 'header', True, 'Generate once per intent and retain on retry. A different intent with the same key conflicts. Authorization is checked before replay; receipts survive restarts.')
def response(schema=None, description='Success'):
    v = {'description': description}
    if schema: v['content'] = {'application/json': {'schema': ref(schema)}}
    return v

def op(path, method, operation, title, tag, output=None, body=None, status=200, auth=TOKEN, params=None, description='', idempotent=False, public_origin=False):
    parameters = list(params or [])
    if auth and path.startswith('/api/v1/') and not path.startswith('/api/v1/integrations/bloem/'):
        if path.startswith('/api/v1/export/') or path.startswith('/api/v1/export-files/'):
            parameters.append(parameter('profile_id',ID,description='Optional authorized tracking profile for browser download links. Never a credential.'))
        parameters.append(parameter('X-Pastime-Profile-ID', ID, 'header', False, 'Authorized active tracking profile. Omitted selects the account default for a session; bearer tokens stay bound to their issued profile.'))
    for name in re.findall(r'\{([^}]+)\}', path):
        parameters.insert(0, parameter(name, string(format='uuid') if name in ('credentialID', 'invitationID', 'connectionID', 'importID', 'jobID', 'fileID', 'authorizationID') else string(minLength=48, maxLength=48) if name == 'secret' else string(pattern='^[a-z0-9][a-z0-9_.-]{2,47}$') if name == 'username' else string(pattern='^[a-z0-9]([a-z0-9-]{0,58}[a-z0-9])?$') if name == 'slug' else OPAQUE if name in ('snapshotID','requestID') else integer(minimum=1900, maximum=9999) if name == 'year' else ID, 'path', True))
    mutating = method != 'get'
    if idempotent: parameters.append(IDEMPOTENCY)
    if mutating and auth and not path.startswith('/api/v1/integrations/bloem/'): parameters.append(CSRF)
    if public_origin: parameters.append(ORIGIN)
    v = {'operationId': operation, 'summary': title, 'tags': [tag], 'security': auth or [], 'responses': {str(status): response(output)}}
    if description: v['description'] = description
    if parameters: v['parameters'] = parameters
    if body: v['requestBody'] = {'required': True, 'content': {'application/json': {'schema': ref(body)}}}
    if tag != 'Runtime':
        for code in (401, 403, 404, 409, 422, 429, 500, 503):
            v['responses'][str(code)] = response('Problem', {401: 'Missing, inactive, expired or revoked credentials', 403: 'Insufficient scope/role or rejected CSRF/origin', 404: 'Record unavailable or not owned by caller', 409: 'Stale version, changed replay intent, duplicate identity or last-owner protection', 422: 'Invalid request or state transition', 429: 'Rate or password-work concurrency limit; Retry-After header is provided', 500: 'Internal failure; no sensitive details disclosed', 503: 'Required service unavailable'}[code])
        if body: v['responses']['415'] = response('Problem', 'JSON content type required')
    paths.setdefault(path, {})[method] = v

op('/api/v1/profiles','get','getTrackingProfiles','List authorized active tracking profiles','Accounts','TrackingProfilePage')
op('/api/v1/profile-logins','get','getProfileLogins','List the profile logins of the household','Accounts','ProfileLoginPage',auth=SESSION,description='The account\'s own browser session only. Passwords are never returned.')
op('/api/v1/profiles/{profileID}/login','put','setProfileLogin','Turn on, rename or reset a profile\'s own sign-in','Accounts','ProfileLogin','ProfileLoginInput',auth=SESSION,description='The account\'s own browser session only, for an active non-default profile. A new password signs the profile person out of every browser. A name already used by an account or another login returns 409. Ten changes per minute. Audited without the password.')
op('/api/v1/profiles/{profileID}/login','delete','removeProfileLogin','Turn a profile\'s own sign-in off','Accounts',status=204,auth=SESSION,description='The account\'s own browser session only. Ends every session of the login at once. Audited.')
BLOEM_SYNC=[{'BloemSyncCredential': []}]
schemas.update({
 'Empty':obj({}),
 'BloemSnapshotCompleted':obj({'completed':{'type':'boolean','enum':[True]}}),
 'BloemBinding':obj({'instance_id':OPAQUE,'account_id':OPAQUE,'profile_id':OPAQUE,'name':string(minLength=1,maxLength=80)}),
 'BloemMedia':obj({'kind':enum('movie','episode'),'external_ids':{'type':'object','additionalProperties':string(maxLength=80)},'series_external_ids':{'type':'object','additionalProperties':string(maxLength=80)},'season':integer(minimum=0),'episode':integer(minimum=1),'title':string(maxLength=500,description='Episodes only: the episode title. Corroborates the identifiers and numbers; never identifies media on its own.'),'series_title':string(maxLength=500,description='Episodes only: the show title, used to find the show only when none of its identifiers does.'),'air_date':string(pattern='^[0-9]{4}-[0-9]{2}-[0-9]{2}$',description='Episodes only: first air date (YYYY-MM-DD). Corroborates the identifiers and numbers; never identifies media on its own.')},['kind','external_ids']),
 'BloemPlaybackEvent':obj({'version':integer(enum=[1]),'instance_id':OPAQUE,'account_id':OPAQUE,'profile_id':OPAQUE,'entity_id':OPAQUE,'event_id':OPAQUE,'revision':REVISION,'operation':enum('start','pause','stop'),'occurred_at':DATE,'completed':{'type':'boolean'},'consumption_id':OPAQUE,'position_seconds':integer(minimum=0,maximum=10000000),'duration_seconds':integer(minimum=1,maximum=10000000),'media':ref('BloemMedia')},['version','instance_id','account_id','profile_id','entity_id','event_id','revision','operation','occurred_at','completed','position_seconds','duration_seconds','media']),
 'BloemEventResult':obj({'status':enum('applied','stale','no_change'),'play_recorded':{'type':'boolean'}}),
 'BloemPersonalMedia':obj({'kind':enum('movie','series'),'external_ids':{'type':'object','maxProperties':3,'additionalProperties':string(minLength=1,maxLength=80),'description':'One to three stable identifiers. May be empty only when catalog_item_id is sent.'},'catalog_item_id':{**ID,'description':'Optional Pastime catalog identifier of a top-level movie or series, as read from a provider_item_key of the form catalog:<id>. Used only when external_ids is empty.'}},['kind']),
 'BloemPersonalEvent':obj({'version':integer(enum=[1]),'instance_id':OPAQUE,'account_id':OPAQUE,'profile_id':OPAQUE,'entity_id':OPAQUE,'event_id':OPAQUE,'revision':REVISION,'operation':enum('favorite_add','favorite_remove','watchlist_add','watchlist_remove','rating_set','rating_remove'),'occurred_at':DATE,'rating':integer(enum=[2,4,6,8,10]),'position':integer(minimum=0,maximum=2147483647),'media':ref('BloemPersonalMedia')},['version','instance_id','account_id','profile_id','entity_id','event_id','revision','operation','occurred_at','media']),
 'BloemPersonalRead':obj({'kinds':{'type':'array','maxItems':5,'items':enum('favorite','watchlist','rating','watched','progress')},'cursor':string(maxLength=128),'page_token':string(maxLength=128),'page_size':integer(minimum=0,maximum=100)},[]),
 'BloemPersonalRemoteItem':obj({'provider_item_key':string(minLength=9,maxLength=64),'kind':enum('favorite','watchlist','rating','watched','progress'),'media_kind':enum('movie','series','episode'),'external_ids':{'type':'object','additionalProperties':string(maxLength=80)},'series_external_ids':{'type':'object','additionalProperties':string(maxLength=80),'description':'Episodes only: the show, by TMDB, IMDb or TVDB identifier.'},'season_number':integer(minimum=0,maximum=100000),'episode_number':integer(minimum=0,maximum=1000000),'removed':{'type':'boolean'},'rating':integer(enum=[2,4,6,8,10]),'position':integer(minimum=0,maximum=2147483647),'play_count':integer(minimum=1,description='Watched items only: plays recorded outside Bloem.'),'progress_percent':{'type':'number','minimum':0,'exclusiveMaximum':100,'description':'Progress items only: a resume point recorded outside Bloem.'},'changed_at':DATE},['provider_item_key','kind','media_kind','external_ids','removed','changed_at']),
 'BloemPersonalPage':obj({'items':{'type':'array','maxItems':100,'items':ref('BloemPersonalRemoteItem')},'next_page_token':string(maxLength=128),'next_cursor':string(maxLength=128),'complete_snapshot':{'type':'boolean'},'warnings':{'type':'array','maxItems':10,'items':string(maxLength=500)}},['items','next_page_token','next_cursor','complete_snapshot','warnings']),
 'BloemSnapshotPage':obj({'snapshot_id':OPAQUE,'cursor':string(maxLength=128),'next_cursor':string(maxLength=128),'complete':{'type':'boolean'},'profiles':{'type':'array','maxItems':100,'items':ref('BloemProvisionInput')}},['snapshot_id','cursor','next_cursor','complete','profiles']),
 'BloemPageReceipt':obj({'receipt':OPAQUE}),
 'BloemSnapshotComplete':obj({'snapshot_id':OPAQUE,'receipt':OPAQUE}),
})
schemas.update({
 'BloemCalendarEvent':obj({'event_id':string(minLength=1,maxLength=256),'content_id':OPAQUE,'kind':string(minLength=1,maxLength=32),'title':string(minLength=1,maxLength=4096),'episode_title':string(maxLength=4096),'series_id':string(maxLength=128),'season_number':NINT,'episode_number':NINT,'date':string(pattern='^[0-9]{4}-[0-9]{2}-[0-9]{2}$'),'air_at':{**DATE,'nullable':True},'air_timezone':string(maxLength=128),'watched':{'type':'boolean'},'followed':{'type':'boolean'},'favourite':{'type':'boolean'},'watchlist':{'type':'boolean'},'external_ids':{'type':'object','additionalProperties':string(maxLength=128)},'series_external_ids':{'type':'object','additionalProperties':string(maxLength=128)},'artwork_id':string(maxLength=2048),'playable':{'type':'boolean'},'release_type':string(maxLength=64),'badges':STRINGS,'source_instance_id':{**OPAQUE,'readOnly':True},'source_authority_expires_at':{**DATE,'readOnly':True}},['event_id','content_id','kind','title','date']),
 'BloemCalendarInput':obj({'version':integer(enum=[1]),'instance_id':OPAQUE,'account_id':OPAQUE,'profile_id':OPAQUE,'snapshot_id':OPAQUE,'cursor':string(maxLength=128),'next_cursor':string(maxLength=128),'complete':{'type':'boolean'},'start':string(pattern='^[0-9]{4}-[0-9]{2}-[0-9]{2}$'),'end':string(pattern='^[0-9]{4}-[0-9]{2}-[0-9]{2}$'),'visibility_digest':string(minLength=1,maxLength=256),'revision':REVISION,'expires_at':DATE,'events':{'type':'array','maxItems':100,'items':ref('BloemCalendarEvent')}},['version','instance_id','account_id','profile_id','snapshot_id','complete','start','end','visibility_digest','revision','expires_at','events']),
 'BloemCalendarReceipt':obj({'snapshot_id':OPAQUE,'next_cursor':string(maxLength=128),'complete':{'type':'boolean'}}),
 'BloemCalendarPublished':obj({'snapshot_id':OPAQUE,'published':{'type':'boolean','enum':[True]}}),
 'BloemCalendarLease':obj({'instance_id':OPAQUE,'account_id':OPAQUE,'profile_id':OPAQUE,'visibility_digest':string(minLength=1,maxLength=256),'expires_at':DATE},['instance_id','account_id','profile_id','visibility_digest','expires_at']),
 'BloemCalendarInvalidate':obj({'instance_id':OPAQUE,'account_id':OPAQUE,'profile_id':OPAQUE,'visibility_digest':string(minLength=1,maxLength=256)},['instance_id','account_id','profile_id','visibility_digest']),
 'BloemCalendarAccepted':obj({'accepted':{'type':'boolean','enum':[True]}}),
})
op('/api/admin/v1/bloem/instances','get','listBloemHandoffInstances','Registered Bloem instances for playback settings','Admin','BloemHandoffInstancePage',auth=SESSION)
op('/api/admin/v1/bloem/instances/{instanceID}/handoff','get','getBloemHandoff','Administrator playback link settings','Admin','BloemHandoffSettings',auth=SESSION)
op('/api/admin/v1/bloem/instances/{instanceID}/handoff','put','setBloemHandoff','Set administrator playback link settings','Admin','BloemHandoffSettings','BloemHandoffSettings',auth=SESSION)
schemas['BloemAvailabilityView'] = obj({'catalog_id':ID,'status':string(enum=['checking','available','unavailable']),'handoff':ref('BloemHandoffTarget'),'expires_at':DATE},['catalog_id','status'])
schemas['BloemAvailabilityPage'] = items('BloemAvailabilityView')
schemas['BloemAvailabilityRequest'] = obj({'request_id':OPAQUE,'instance_id':OPAQUE,'account_id':OPAQUE,'profile_id':OPAQUE,'kind':string(enum=['movie','series']),'namespace':string(enum=['tmdb','imdb','tvdb']),'external_id':OPAQUE})
schemas['BloemAvailabilityPending'] = items('BloemAvailabilityRequest')
schemas['BloemAvailabilityResult'] = obj({'request_id':OPAQUE,'available':{'type':'boolean'},'content_id':string(maxLength=128)})
schemas['BloemAvailabilityInput'] = obj({'version':integer(enum=[1]),'instance_id':OPAQUE,'account_id':OPAQUE,'profile_id':OPAQUE,'checked_at':DATE,'expires_at':DATE,'results':{'type':'array','minItems':1,'maxItems':100,'items':ref('BloemAvailabilityResult')}})
schemas['BloemAvailabilityReceipt'] = obj({'accepted':{'type':'boolean'}})
op('/api/v1/catalog/{catalogID}/bloem-availability','get','getBloemAvailability','Current profile Bloem availability and administrator-controlled playback links','Catalog','BloemAvailabilityPage',description='Queues a bounded lookup for linked movie/series titles. Results expire within two minutes; checking does not imply availability. Rechecks current profile and service access. Does not grant playback access.')
op('/api/v1/integrations/bloem/availability:pending','post','getPendingBloemAvailability','Read up to one hundred pending source-bound title lookups','Connections','BloemAvailabilityPending',auth=BLOEM_PROVISION)
op('/api/v1/integrations/bloem/availability:apply','post','acceptBloemAvailability','Publish timestamped results for current profile lookup requests','Connections','BloemAvailabilityReceipt','BloemAvailabilityInput',auth=BLOEM_PROVISION)
schemas['BloemRecommendationsReceipt'] = obj({'accepted':{'type':'boolean'}})
schemas['BloemAIRecommendation'] = obj({'content_id':OPAQUE,'kind':string(enum=['movie','series']),'title':string(maxLength=4096),'reason':string(maxLength=4096),'reason_detail':string(maxLength=4096),'external_ids':{'type':'object','additionalProperties':string(maxLength=128)}})
schemas['BloemRecommendationsInput'] = obj({'version':integer(enum=[1]),'instance_id':OPAQUE,'account_id':OPAQUE,'profile_id':OPAQUE,'generated_at':DATE,'expires_at':DATE,'items':{'type':'array','maxItems':100,'items':ref('BloemAIRecommendation')}})
schemas['AIRecommendation'] = obj({**schemas['BloemAIRecommendation']['properties'],'source_instance_id':OPAQUE,'source_authority_expires_at':DATE,'catalog_id':ID,'image_url':string(),'backdrop_url':string()},schemas['BloemAIRecommendation']['required']+['source_instance_id','source_authority_expires_at','image_url','backdrop_url'])
schemas['AIRecommendationPage'] = items('AIRecommendation')
op('/api/v1/integrations/bloem/recommendations','post','acceptBloemRecommendations','Publish the linked profile AI projection','Connections','BloemRecommendationsReceipt','BloemRecommendationsInput',auth=BLOEM_PROVISION)
schemas['RecommendationChoice'] = obj({'source_instance_id':OPAQUE,'content_id':OPAQUE,'dismissed':{'type':'boolean'}})
schemas['RecommendationFeedback'] = obj({**schemas['RecommendationChoice']['properties'],'account_id':OPAQUE,'profile_id':OPAQUE,'revision':integer(minimum=1),'title':string(maxLength=4096)})
schemas['RecommendationFeedbackPage'] = items('RecommendationFeedback')
op('/api/v1/ai-recommendations/feedback','put','setAIRecommendationFeedback','Persist a profile-scoped AI dismissal or undo','Insights',None,'RecommendationChoice',status=204)
op('/api/v1/ai-recommendations/feedback','get','listAIRecommendationFeedback','Your hidden AI recommendations','Insights','RecommendationFeedbackPage')
op('/api/v1/integrations/bloem/recommendations/feedback:pending','post','pendingBloemRecommendationFeedback','Read bounded current feedback for the source profile','Connections','RecommendationFeedbackPage',auth=BLOEM_PROVISION)
op('/api/v1/integrations/bloem/recommendations/feedback:ack','post','ackBloemRecommendationFeedback','Acknowledge exactly one delivered feedback revision','Connections','BloemRecommendationsReceipt','RecommendationFeedback',auth=BLOEM_PROVISION)
op('/api/v1/ai-recommendations','get','getAIRecommendations','Personal AI recommendations shared from the linked Bloem profile','Insights','AIRecommendationPage')
schemas['BloemLibraryQuery'] = obj({'instance_id':OPAQUE,'view':enum('catalog','collections','collection_items','copies','book_states'),'parent_id':string(maxLength=256),'cursor':string(maxLength=16384),'limit':integer(minimum=1,maximum=100),'query':string(maxLength=256),'kind':enum('','movie','series','ebook','audiobook')})
schemas['BloemLibraryRequest'] = obj({**schemas['BloemLibraryQuery']['properties'],'request_id':OPAQUE,'account_id':OPAQUE,'profile_id':OPAQUE})
schemas['BloemLibraryPending'] = items('BloemLibraryRequest')
schemas['BloemLibraryItem'] = obj({'content_id':OPAQUE,'kind':enum('movie','series','ebook','audiobook'),'title':string(maxLength=4096),'external_ids':{'type':'object','additionalProperties':string(maxLength=128)},'year':integer(),'available':{'type':'boolean'},'handoff':ref('BloemHandoffTarget'),'catalog_id':ID,'image_url':string()},['content_id','kind','title','external_ids','year','available'])
schemas['BloemLibraryCollection'] = obj({'id':OPAQUE,'name':string(),'description':string(),'scope':{'type':'string','enum':['personal','shared','server'],'x-enum-varnames':['BloemCollectionScopePersonal','BloemCollectionScopeShared','BloemCollectionScopeServer']},'writable':{'type':'boolean'},'revision':REVISION,'definition_kind':string()})
schemas['BloemLibraryCopy'] = obj({'id':OPAQUE,'content_id':string(),'kind':enum('movie','series','ebook','audiobook'),'title':string(maxLength=4096),'external_ids':{'type':'object','additionalProperties':string(maxLength=128)},'format':string(),'edition':string(),'resolution':string(),'hdr':string(),'audio':string(),'bitrate_kbps':integer(minimum=0),'acquired_on':string(),'price_cents':integer(minimum=0),'currency':string(),'store':string(),'location':string(),'notes':string(),'custom':{'type':'object','additionalProperties':string()},'removed':{'type':'boolean'},'revision':integer(minimum=0)})
schemas['BloemBookState'] = obj({'content_id':OPAQUE,'kind':enum('ebook','audiobook'),'title':string(maxLength=4096),'external_ids':{'type':'object','additionalProperties':string(maxLength=128)},'edition_id':OPAQUE,'isbn':string(),'file_id':integer(minimum=1),'audiobook_seconds':{'type':'number','minimum':0},'audiobook_duration_seconds':{'type':'number','minimum':0},'ebook_location':string(maxLength=16384),'ebook_fraction':{'type':'number','minimum':0,'maximum':1},'completed':{'type':'boolean'},'reset':{'type':'boolean'},'changed_at':{**DATE,'nullable':True},'revision':integer(minimum=0)})
schemas['BloemLibraryInput'] = obj({'request_id':OPAQUE,'instance_id':OPAQUE,'account_id':OPAQUE,'profile_id':OPAQUE,'view':enum('catalog','collections','collection_items','copies','book_states'),'parent_id':string(),'next_cursor':string(maxLength=16384),'complete':{'type':'boolean'},'generated_at':DATE,'expires_at':DATE,'items':array('BloemLibraryItem'),'collections':array('BloemLibraryCollection'),'copies':array('BloemLibraryCopy'),'book_states':array('BloemBookState'),'revision':integer(minimum=0)})
schemas['BloemLibraryResult'] = obj({'request_id':OPAQUE,'status':enum('pending','ready','expired','denied','invalid'),'page':ref('BloemLibraryInput')},['request_id','status'])
schemas['BloemLibraryFailure'] = obj({'request_id':OPAQUE,'reason':enum('denied','invalid')})
op('/api/v1/integrations/bloem/library:reject','post','rejectBloemLibrary','Report a source refusal without publishing metadata','Connections','BloemAvailabilityReceipt','BloemLibraryFailure',auth=BLOEM_PROVISION)
# Native list export is machine-only and independently specified. It exposes
# identity metadata, never private tracking state or annotations.
LIST_BOUND = {'instance_id':OPAQUE,'account_id':OPAQUE,'profile_id':OPAQUE}
LIST_HASH = string(pattern='^[0-9a-f]{64}$')
schemas['BloemListResolveInput'] = obj({**LIST_BOUND,'url':string(maxLength=4096)})
schemas['BloemListResolution'] = obj({'signing_key':string(minLength=43,maxLength=43,pattern='^[A-Za-z0-9_-]{43}$'),'source_id':OPAQUE,'name':string(maxLength=500),'kind':enum('manual','smart','collaborative'),'count':integer(minimum=0,maximum=10000),'resolution_token':string(maxLength=16384),'expires_at':DATE})
schemas['BloemListSnapshotInput'] = obj({**LIST_BOUND,'source_id':OPAQUE,'resolution_token':string(maxLength=16384),'cursor':string(maxLength=16384)},list(LIST_BOUND)+['source_id','resolution_token'])
schemas['BloemListSnapshotEntry'] = obj({'kind':enum('movie','series','ebook','audiobook','unsupported'),'title':string(maxLength=1000),'year':integer(minimum=0,maximum=9999),'external_ids':{'type':'object','additionalProperties':string(maxLength=64),'maxProperties':5},'source_entry_id':OPAQUE,'unsupported_reason':enum('missing_identity','unsupported_kind','invalid_identity','ambiguous_identity')},['kind','title','year','external_ids'])
schemas['BloemListSnapshotPage'] = obj({'version':integer(minimum=1,maximum=1),'source_id':OPAQUE,'snapshot_id':OPAQUE,'position':integer(minimum=0,maximum=10000),'total':integer(minimum=0,maximum=10000),'digest':LIST_HASH,'generated_at':DATE,'expires_at':DATE,'next_cursor':string(maxLength=16384),'final':{'type':'boolean'},'entries':{'type':'array','maxItems':100,'items':ref('BloemListSnapshotEntry')},'source_attestation':string(maxLength=16384)})
schemas['BloemListAttestationInput'] = obj({**schemas['BloemListSnapshotInput']['properties'],'snapshot_id':OPAQUE},schemas['BloemListSnapshotInput']['required']+['snapshot_id'])
schemas['BloemListAttestation'] = obj({'snapshot_id':OPAQUE,'digest':LIST_HASH,'source_attestation':string(maxLength=16384),'expires_at':DATE})
op('/api/v1/integrations/bloem/lists:resolve','post','resolveBloemSourceList','Resolve an authorized native list URL to a stable source identity','Connections','BloemListResolution','BloemListResolveInput',auth=BLOEM_PROVISION,description='Only URLs on this configured Pastime origin. Native private lists require the mapped profile owner; collaborative lists require actual membership. Public or unlisted lists retain current visibility/token checks. The resolution token is encrypted and scoped to the mapped source identity. No private tracking data is exported.')
op('/api/v1/integrations/bloem/lists:page','post','readBloemSourceListSnapshot','Read immutable ordered identity metadata for an authorized list','Connections','BloemListSnapshotPage','BloemListSnapshotInput',auth=BLOEM_PROVISION,description='First request materializes one consistent snapshot. Continuations page persisted rows with fresh source and profile authority. Limits:100 per page,10,000 titles,8 MiB,120-second lease and eight active snapshots per profile. Unsupported identities retain their source positions with explicit reason codes. Never truncate or mix revisions.')
op('/api/v1/integrations/bloem/lists:attest','post','attestBloemSourceListSnapshot','Recheck snapshot source authority before host publication','Connections','BloemListAttestation','BloemListAttestationInput',auth=BLOEM_PROVISION,description='Checks the same installation, mapped profile, stable source grant, native membership/visibility and unexpired snapshot. Does not extend snapshot expiry.')
schemas['BloemLibrarySource'] = obj({'instance_id':OPAQUE,'name':string()})
schemas['BloemLibrarySources'] = items('BloemLibrarySource')
op('/api/v1/bloem/library/sources','get','getBloemLibrarySources','Your active connected media servers','Library','BloemLibrarySources')
op('/api/v1/bloem/library/queries','post','queryBloemLibrary','Request one profile-authorized media page','Library','BloemLibraryResult','BloemLibraryQuery')
op('/api/v1/bloem/library/queries/{requestID}','get','getBloemLibraryPage','Read your requested media page within its source lease','Library','BloemLibraryResult')
op('/api/v1/integrations/bloem/library:pending','post','getPendingBloemLibrary','Read pending source-bound library pages','Connections','BloemLibraryPending',auth=BLOEM_PROVISION)
op('/api/v1/integrations/bloem/library:apply','post','acceptBloemLibrary','Publish exactly one requested library page','Connections','BloemAvailabilityReceipt','BloemLibraryInput',auth=BLOEM_PROVISION)
schemas['BloemNativeCopyImport'] = obj({'request_id':OPAQUE,'copy_id':OPAQUE,'entry_id':ID})
op('/api/v1/bloem/library/copies:import','post','importBloemOwnedCopy','Explicitly import a connected owned copy; scope library:write','Collection','Mutation','BloemNativeCopyImport',description='Requires a current source copies page and exact catalog identity match to your entry. Preserves technical and custom values or rejects unsupported conversion atomically. Repeated source revisions reuse the same copy. Local edits or deletion conflict; browsing availability never creates ownership.')
schemas['BloemLibraryMutationIntent'] = obj({'instance_id':OPAQUE,'event_id':OPAQUE,'operation':enum('collection.create','collection.update','collection.delete','collection.add','collection.remove','copy.upsert','copy.remove','book.progress','book.completed','book.reset'),'target_id':string(),'expected_revision':integer(minimum=0),'name':string(maxLength=200),'description':string(maxLength=10000),'items':array('BloemLibraryItem'),'copy':ref('BloemLibraryCopy'),'book_state':ref('BloemBookState')},['instance_id','event_id','operation','target_id','expected_revision','name','description','items'])
schemas['BloemLibraryMutationRequest'] = obj({**schemas['BloemLibraryMutationIntent']['properties'],'request_id':OPAQUE,'account_id':OPAQUE,'profile_id':OPAQUE},schemas['BloemLibraryMutationIntent']['required']+['request_id','account_id','profile_id'])
schemas['BloemLibraryMutationPending'] = items('BloemLibraryMutationRequest')
schemas['BloemLibraryMutationResult'] = obj({'request_id':OPAQUE,'event_id':OPAQUE,'target_id':string(),'revision':integer(minimum=0),'status':enum('pending','applied','no_change','removed','conflict','denied','invalid')})
op('/api/v1/bloem/library/mutations','post','requestBloemCollectionChange','Queue a revision-checked collection change','Library','BloemLibraryMutationResult','BloemLibraryMutationIntent')
op('/api/v1/bloem/library/mutations/{requestID}','get','getBloemCollectionChange','Read the confirmed outcome of your collection change','Library','BloemLibraryMutationResult')
op('/api/v1/integrations/bloem/library/mutations:pending','post','getPendingBloemCollectionChanges','Read pending source-bound collection changes','Connections','BloemLibraryMutationPending',auth=BLOEM_PROVISION)
op('/api/v1/integrations/bloem/library/mutations:apply','post','confirmBloemCollectionChange','Confirm an exact source collection mutation receipt','Connections','BloemAvailabilityReceipt','BloemLibraryMutationResult',auth=BLOEM_PROVISION)
op('/api/v1/integrations/bloem/calendar/pages','post','acceptBloemCalendarPage','Stage one profile-authorized calendar page','Connections','BloemCalendarReceipt','BloemCalendarInput',auth=BLOEM_PROVISION)
op('/api/v1/integrations/bloem/calendar/{snapshotID}/publish','post','publishBloemCalendar','Publish a complete calendar window','Connections','BloemCalendarPublished',auth=BLOEM_PROVISION)
op('/api/v1/integrations/bloem/calendar/leases','post','renewBloemCalendarLease','Renew a freshly verified source calendar lease','Connections','BloemCalendarAccepted','BloemCalendarLease',auth=BLOEM_PROVISION)
op('/api/v1/integrations/bloem/calendar/invalidate','post','invalidateBloemCalendar','Hide an unauthorized source calendar projection','Connections','BloemCalendarAccepted','BloemCalendarInvalidate',auth=BLOEM_PROVISION)
op('/api/v1/integrations/bloem/credentials:inspect','post','inspectBloemCredential','Inspect a source-bound profile credential','Connections','BloemBinding','Empty',auth=BLOEM_SYNC)
op('/api/v1/integrations/bloem/events:apply','post','applyBloemPlayback','Apply authoritative profile playback','Connections','BloemEventResult','BloemPlaybackEvent',auth=BLOEM_SYNC,description='One event per request. Stable media identifiers; an episode title and air date only corroborate them. Source binding, exact replay receipts, source revisions and viewing provenance protect independent profile state.')
op('/api/v1/integrations/bloem/state/events:apply','post','applyBloemPersonalState','Apply profile-bound favorites, watchlists and exact ratings','Connections','BloemEventResult','BloemPersonalEvent',auth=BLOEM_SYNC,description='Source-bound receipts and revisions prevent changed replay and stale state. Unrepresentable native ratings are preserved and report rating_precision.')
op('/api/v1/integrations/bloem/state:read','post','readBloemPersonalState','Read durable profile personal-state pages','Connections','BloemPersonalPage','BloemPersonalRead',auth=BLOEM_SYNC,description='Profile and credential-bound snapshots expire after ten minutes. Continue only through issued page tokens. Missing memberships can be removed only from a complete snapshot; ratings with no exact Bloem equivalent pause ratings synchronization. Watched and progress items never include plays or resume points Pastime received from Bloem.')
SYNC_IDS={'type':'object','maxProperties':3,'additionalProperties':string(minLength=1,maxLength=80)}
schemas.update({
 'SiloConnectProfile':obj({'profile_id':ID,'name':string(),'connected':{'type':'boolean','description':'A current, unexpired Silo key exists for this profile.'},'expires_at':{**NDATE,'description':'When the current key expires unless Silo keeps using it.'}}),
 'SiloConnect':obj({'available':{'type':'boolean','description':'A Silo server is registered and keys can be created.'},'service_access':{'type':'boolean'},'silo_url':string(description='Address of the registered Silo server; empty when none or more than one.'),'profiles':{'type':'array','items':ref('SiloConnectProfile')}}),
 'SiloCredentialInput':obj({'instance_id':{**OPAQUE,'description':'Needed only when more than one Silo server is registered.'}},[]),
 'SiloCredential':obj({'credential':string(format='password',description='Shown once. Paste it into Silo; never store it in the browser.'),'expires_at':DATE,'profile_name':string()}),
})
op('/api/v1/integrations/silo/connect','get','getSiloConnect','Your tracking profiles and whether each one is connected to Silo','Connections','SiloConnect',auth=SESSION)
op('/api/v1/integrations/silo/profiles/{profileID}/credential','post','createSiloCredential','Create the Silo key for one of your tracking profiles','Connections','SiloCredential','SiloCredentialInput',auth=SESSION,description='Returns the key once. A profile has one current key: a new key replaces the previous one as soon as Silo uses it, and after 24 hours at the latest. Keys stay valid while Silo keeps using them. Requires tracking access.')
op('/api/v1/integrations/silo/profiles/{profileID}/credential','delete','revokeSiloCredential','Disconnect one of your tracking profiles from Silo','Connections',status=204,auth=SESSION,description='Revokes every Silo key of the profile at once.')
op('/api/v1/integrations/bloem/snapshots:page','post','acceptBloemSnapshotPage','Accept a fully provisioned snapshot page','Connections','BloemPageReceipt','BloemSnapshotPage',auth=BLOEM_PROVISION)
op('/api/v1/integrations/bloem/snapshots:complete','post','completeBloemSnapshot','Finalize a traversed snapshot','Connections','BloemSnapshotCompleted','BloemSnapshotComplete',auth=BLOEM_PROVISION,description='Requires the final server-issued receipt. Partial or expired traversals cannot archive mappings. Newer mappings and linked login eligibility are preserved.')
op('/api/v1/integrations/bloem/profiles:provision','post','provisionBloemProfile','Provision or update a source-bound managed profile','Connections','BloemProvisionResult','BloemProvisionInput',auth=BLOEM_PROVISION,description='Backend connector only. Member creation, stable instance/account/profile IDs, independent account/profile revisions. Names and email never establish identity ownership. Cookies, personal tokens, sync credentials and profile override headers do not grant provisioning authority.')
op('/api/v1/integrations/bloem/profiles/{profileID}/credentials:issue','post','issueBloemProfileCredential','Issue a retry-stable backend tracking credential','Connections','BloemCredentialResult','BloemCredentialInput',auth=BLOEM_PROVISION,description='Backend connector only. Issuance ID replay returns the same encrypted credential; a new issuance rotates and revokes previous grants. Inactive or excluded source mappings cannot receive credentials. No-store response.')


schemas.update({
    'SavedViewInput': obj({'name': string(minLength=1,maxLength=80), 'rules':ref('Rules'), 'sort':enum('added','activity','title','rating','year','played'), 'layout':enum('grid','list'), 'position':integer(minimum=0,maximum=100000), 'version':VERSION}, ['name','rules','sort','layout']),
    'SavedView': obj({'id':ID,'name':string(),'rules':ref('Rules'),'sort':enum('added','activity','title','rating','year','played'),'layout':enum('grid','list'),'position':integer(),'version':VERSION}),
    'SavedViewPage':items('SavedView'),
    'ViewOrder':obj({'ids':{'type':'array','items':ID,'maxItems':100,'uniqueItems':True}}),
})
op('/api/v1/saved-views','get','getSavedViews','List your saved library views','Library','SavedViewPage')
op('/api/v1/saved-views','post','createSavedView','Save library filters, sort and layout','Library','SavedView','SavedViewInput')
op('/api/v1/saved-views/{viewID}','put','updateSavedView','Update a saved view with version checking','Library','SavedView','SavedViewInput')
op('/api/v1/saved-views/{viewID}','delete','deleteSavedView','Delete your saved view','Library',body='VersionInput',status=204)
op('/api/v1/saved-views/order','put','orderSavedViews','Reorder the complete current saved-view set','Library',body='ViewOrder',status=204)

schemas.update({
 'StatusChange':obj({'id':ID,'previous_status':string(nullable=True),'status':STATUS,'source':string(),'created_at':DATE}),
 'StatusChangePage':paged('StatusChange'),
 'EpisodeRating':obj({'catalog_id':ID,'title':string(),'season':integer(nullable=True),'episode':integer(nullable=True),'rating':integer(nullable=True,minimum=0,maximum=10),'scored_at':NDATE,'version':integer(minimum=0)}),
 'EpisodeRatingPage':paged('EpisodeRating'),
 'EpisodeRatingInput':obj({'rating':integer(nullable=True,minimum=0,maximum=10),'version':integer(minimum=0)}),
 'DerivedRating':obj({'rating':{'type':'number','nullable':True},'rated_episodes':integer(),'watched_episodes':integer()}),
})
op('/api/v1/library/{entryID}/status-history','get','getStatusHistory','Read private tracking status provenance','Library','StatusChangePage',params=[parameter('cursor',string())])
op('/api/v1/library/{entryID}/episode-ratings','get','getEpisodeRatings','Page distinct watched episode ratings','Library','EpisodeRatingPage',params=[parameter('cursor',string())])
op('/api/v1/library/{entryID}/derived-rating','get','getDerivedRating','Average distinct watched episode ratings without counting rewatches twice','Library','DerivedRating',params=[parameter('season',integer(minimum=0,maximum=10000))])
op('/api/v1/library/{entryID}/episode-ratings/{catalogID}','put','rateEpisode','Rate a watched episode with optimistic version checking','Library','EpisodeRating','EpisodeRatingInput')

op('/healthz','get','getLiveness','Process liveness without dependency I/O','Runtime','Liveness',auth=None)
op('/readyz','get','getReadiness','Bounded database/schema and optional Valkey checks','Runtime','Readiness',auth=None)
paths['/readyz']['get']['responses']['503'] = response('Readiness','Required database/schema check failed')
op('/api/v1/info','get','getServerInfo','Non-secret server identity and implementation stage','Runtime','ServerInfo',auth=None)
op('/api/v1/register','post','register','Create a local member account and sign in','Account','SessionView','RegistrationInput',status=201,auth=None,public_origin=True,description='Available only when PUBLIC_REGISTRATION=true. Accounts always receive member access. Contact email is not proof of ownership and never links another identity. Five attempts per client address per minute.')
op('/api/v1/login','post','login','Sign in; set a seven-day HttpOnly session cookie','Account','SessionView','LoginInput',auth=None,public_origin=True,description='Credentials are verified with Argon2id and bounded work. Invalid credentials and inactive accounts share the same response. A maximum of ten active sessions is retained. Forwarded IP headers are not trusted.')
op('/api/v1/session','get','getSession','Current browser session and session-bound CSRF token','Account','SessionView',auth=SESSION)
op('/api/v1/me','get','getMe','Current account; token scope profile:read','Account','User')
op('/api/v1/logout','post','logout','Revoke this browser session and clear its cookie','Account',status=204,auth=SESSION)
op('/api/v1/me','patch','updateProfile','Update your display name or contact email','Account','User','UserPatch',auth=SESSION,description='Role and active fields are forbidden here. Email is contact metadata, not a verified login/recovery identity.')
op('/api/v1/me/password','post','changePassword','Change password and revoke all current access','Account',body='PasswordInput',status=204,auth=SESSION,description='Requires the current password. Successful change revokes all sessions, tokens and invitations issued by the account; sign in again.')
op('/api/v1/me/sessions','get','getSessions','List up to ten active sessions','Account','SessionPage',auth=SESSION)
op('/api/v1/me/sessions/{credentialID}','delete','revokeSession','Revoke one of your sessions','Account',status=204,auth=SESSION)
op('/api/v1/me/tokens','get','getTokens','List your most recent 100 token records; never their secrets','Account','TokenPage',auth=SESSION)
op('/api/v1/me/tokens','post','createToken','Create an expiring personal token; show its secret once','Account','TokenCreated','TokenInput',201,SESSION,description='Requires current password. At most 20 unexpired, unrevoked tokens. No token can administer accounts or mint further credentials, regardless of its owner role.')
op('/api/v1/me/tokens/{credentialID}','delete','revokeToken','Revoke one of your personal tokens','Account',status=204,auth=SESSION)
op('/api/v1/invitations/preview','post','previewInvitation','Inspect an available invitation without consuming it','Invitations','InvitePreview','InviteTokenInput',auth=None,public_origin=True,description='Send the secret in the JSON body, not a URL/query string. No recipient email is disclosed.')
op('/api/v1/invitations/claim','post','claimInvitation','Atomically consume a one-use invitation or password-reset link','Invitations','User','ClaimInput',201,None,public_origin=True,description='Join links require a username. Reset links ignore username/display_name and preserve the target role. A claim never signs the browser in. Reset claims revoke old access. Issuer authority, recipient status and expiry are rechecked at claim time. A recipient email is informational and not proof of email ownership.')
op('/api/admin/v1/users','get','getUsers','Browse account metadata without private media/history','Administration','UserPage',auth=SESSION,params=PAGINATION+[parameter('q',string(maxLength=48),description='Case-insensitive username prefix.'),parameter('role',ROLE),parameter('status',enum('active','disabled'))],description='Admin/owner only. Every result is bounded and cursor-paginated.')
op('/api/admin/v1/users','post','createUser','Create an account and a one-use password setup link','Administration','UserCreated','CreateUserInput',201,SESSION,description='Admins manage members only; owners manage every role. No reusable temporary password is generated. A pending account cannot sign in and does not count as a remaining usable owner. Deliver the displayed link privately; email transport is not implemented.')
op('/api/admin/v1/users/{userID}','get','getUser','Read account metadata only','Administration','User',auth=SESSION,description='Admin/owner only. Does not expose library, lists, notes, tokens or password hashes.')
op('/api/admin/v1/users/{userID}','patch','updateUser','Update account metadata, role or active state','Administration','User','UserPatch',auth=SESSION,description='Admin can manage members only. Owner manages all roles. Requires current version. Role/status changes revoke sessions, tokens and outstanding invitations issued by this account. Never removes the final active password-ready owner.')
op('/api/admin/v1/users/{userID}/reset-link','post','createResetLink','Issue a one-hour password-reset link without locking out the account','Administration','InviteCreated',status=201,auth=SESSION,description='Same role boundary as account updates. Revokes older unclaimed reset links for this target. Existing access is revoked only after successful claim.')
op('/api/admin/v1/users/{userID}/revoke-access','post','revokeUserAccess','Revoke all sessions/tokens and issued invitations','Administration',status=204,auth=SESSION,description='Same role boundary as account updates. This does not change the password; an active account can sign in again. Disable the account to block new sign-ins.')
op('/api/admin/v1/invitations','get','getInvitations','Read the latest 100 invitations without secrets','Administration','InvitationPage',auth=SESSION,description='Admin/owner only. This is a bounded recent-history view, not a full export.')
op('/api/admin/v1/invitations','post','createInvitation','Create an invitation and display its secret link once','Administration','InviteCreated','InviteInput',201,SESSION,description='Admin invites members; owner may invite all roles. Up to 100 currently pending join/reset links. Copy-link delivery only; no email is sent. Recipient email is contact metadata, not enforced ownership proof.')
op('/api/admin/v1/invitations/{invitationID}','delete','revokeInvitation','Revoke an unclaimed invitation within your role authority','Administration',status=204,auth=SESSION)
op('/api/admin/v1/audit','get','getAudit','Cursor-paginated account/security audit; no media content or secrets','Administration','AuditPage',auth=SESSION,params=PAGINATION)
NUM = {'type': 'number'}
NTIME = {**string(format='date-time'), 'nullable': True}
UUID = string(format='uuid')
JOB_STATUS = enum('queued', 'running', 'succeeded', 'failed', 'blocked')
IMPORT_STATUS = enum('queued', 'running', 'succeeded', 'partial_success', 'failed', 'cancelled', 'rolled_back')
schemas.update({
    'HostStats': obj({'host': string(), 'requests': integer(), 'failures': integer(), 'rate_limited': integer(), 'total_latency_ms': integer(), 'last_status': integer(), 'last_at': DATE, 'last_failure_at': DATE}),
    'PoolStats': obj({'max': integer(), 'total': integer(), 'idle': integer(), 'acquired': integer()}),
    'NodeStats': obj({'goroutines': integer(), 'heap_mb': NUM, 'go_version': string(), 'pool': ref('PoolStats'), 'providers': array('HostStats'), 'concurrency': integer()}, ['goroutines', 'heap_mb', 'go_version', 'pool', 'providers']),
    'RuntimeNode': obj({'id': UUID, 'role': {**enum('server', 'worker'), 'x-enum-varnames': ['NodeRoleServer', 'NodeRoleWorker']}, 'hostname': string(), 'version': string(), 'started_at': DATE, 'seen_at': DATE, 'stats': ref('NodeStats')}),
    'DatabaseHealth': obj({'version': string(), 'size_bytes': integer(), 'connections': integer(), 'max_connections': integer(), 'migration': integer()}),
    'InstanceCounts': obj({k: integer(minimum=0) for k in ['users', 'active_users', 'entries', 'plays', 'catalog_titles', 'catalog_records', 'active_imports', 'connections']}),
    'JobHour': obj({'hour': DATE, 'succeeded': integer(minimum=0), 'failed': integer(minimum=0)}),
    'JobHealth': obj({'queued': integer(minimum=0), 'running': integer(minimum=0), 'failed': integer(minimum=0), 'blocked': integer(minimum=0), 'oldest_queued_seconds': integer(nullable=True), 'throughput': array('JobHour'), 'failed_last_day': integer(minimum=0)}),
    'ProviderHealth': obj({'name': string(), 'configured': {'type': 'boolean'}, 'kinds': STRINGS, 'requests': integer(), 'failures': integer(), 'rate_limited': integer(), 'mean_latency_ms': integer(nullable=True), 'last_at': NTIME, 'last_failure_at': NTIME, 'credential': string(description='Setting names that enable the provider; never credential values.'), 'defaults': STRINGS, 'search': ref('QueryCapabilities'), 'browse': ref('QueryCapabilities'), 'health': enum('unknown', 'available', 'temporarily_unavailable', 'not_configured'), 'setup_hint': string(description='Nonsecret setup guidance. TVDB subscriber PIN requirements depend on key licence. Health reflects observed traffic, not a current live probe.')}),
    'AdminOverview': obj({'generated_at': DATE, 'nodes': array('RuntimeNode'), 'database': ref('DatabaseHealth'), 'counts': ref('InstanceCounts'), 'jobs': ref('JobHealth'), 'providers': array('ProviderHealth')}),
    'AdminJob': obj({'id': UUID, 'kind': string(), 'status': JOB_STATUS, 'attempts': integer(minimum=0), 'max_attempts': integer(minimum=1), 'run_after': DATE, 'last_error': string(nullable=True), 'created_at': DATE, 'updated_at': DATE}),
    'AdminJobPage': paged('AdminJob'),
    'JobKindCount': obj({'kind': string(), 'queued': integer(), 'running': integer(), 'succeeded': integer(), 'failed': integer(), 'blocked': integer()}),
    'JobKindPage': items('JobKindCount'),
    'CatalogKindStats': obj({'kind': KIND, 'provider': string(), 'titles': integer(), 'tracked': integer(), 'stale': integer()}),
    'CatalogStats': obj({'records': integer(), 'kinds': array('CatalogKindStats')}),
    'RefreshScheduled': obj({'scheduled': integer(minimum=0)}),
    'AdminImportRun': obj({'id': UUID, 'username': string(), 'source': string(), 'status': IMPORT_STATUS, 'total': integer(), 'processed': integer(), 'imported': integer(), 'plays_added': integer(), 'unmatched': integer(), 'failed': integer(), 'created_at': DATE, 'finished_at': NTIME}),
    'AdminImportPage': paged('AdminImportRun'),
})
op('/api/admin/v1/overview','get','getAdminOverview','Instance health in one request: processes, database, counts, job queue and providers','Administration','AdminOverview',auth=SESSION,description='Cached for ten seconds. Large-table counts are planner estimates. Processes report every 30 seconds; one silent for 90 seconds is omitted. Provider figures are summed across processes since each started.')
op('/api/admin/v1/jobs','get','getAdminJobs','Background jobs, newest first','Administration','AdminJobPage',auth=SESSION,params=PAGINATION+[parameter('status',JOB_STATUS),parameter('kind',string(maxLength=100))],description='Payloads and deduplication keys are never shown: they can identify the account the work is for.')
op('/api/admin/v1/job-kinds','get','getAdminJobKinds','Job counts by kind and status','Administration','JobKindPage',auth=SESSION,description='Every unfinished job plus jobs that succeeded in the last seven days.')
op('/api/admin/v1/jobs/{jobID}/retry','post','retryAdminJob','Requeue a failed or blocked job with a fresh attempt budget','Administration','AdminJob',auth=SESSION,description='A blocked job stopped because replaying it could repeat an outside effect; retrying it is your decision and is audited. 409 job_state for any other status.')
op('/api/admin/v1/jobs/{jobID}/cancel','post','cancelAdminJob','Cancel a queued or blocked job','Administration','AdminJob',auth=SESSION,description='The job is marked failed with cancelled_by_admin. Running jobs cannot be cancelled. Audited.')
op('/api/admin/v1/catalog','get','getAdminCatalog','Shared catalog size by type and provider','Administration','CatalogStats',auth=SESSION,description='records is a planner estimate. stale counts tracked titles not refreshed for seven days.')
op('/api/admin/v1/catalog/refresh-stale','post','refreshStaleCatalog','Schedule a metadata refresh of tracked titles that are due','Administration','RefreshScheduled',auth=SESSION,description='At most 200 titles per call; the scheduler does the same every 30 minutes. Audited.')
op('/api/admin/v1/imports','get','getAdminImports','Import runs across accounts, as counts only','Administration','AdminImportPage',auth=SESSION,params=PAGINATION+[parameter('status',IMPORT_STATUS)],description='No titles, unmatched samples or files are exposed.')
schemas.update({
    'SetupCheck': obj({'id': string(), 'title': string(), 'status': enum('ok', 'warning', 'error'), 'detail': string()}),
    'SetupCheckPage': items('SetupCheck'),
})
op('/api/admin/v1/checks','get','getSetupChecks','Configuration checks for this server','Administration','SetupCheckPage',auth=SESSION,description='Each check explains what to change. No secret values are included.')
LIBRARY_FILTERS = [
    parameter('q',string(maxLength=100),description='PostgreSQL simple-config word search over your titles, not substring/provider search.'),
    parameter('media_type',string(maxLength=200),description='One or more media types, comma-separated.'),
    parameter('status',string(maxLength=100),description='One or more statuses, comma-separated.'),
    parameter('tag_id',string(maxLength=400),description='Tag ids, comma-separated; an entry must carry every one.'),
    parameter('genre',string(maxLength=600),description='Catalog genres, comma-separated; any may match.'),
    parameter('min_rating',integer(minimum=0,maximum=10)),parameter('year_from',integer(minimum=1,maximum=9999)),parameter('year_to',integer(minimum=1,maximum=9999)),
    parameter('owned',{'type':'boolean'},description='true: only titles with an owned copy; false: only titles without.'),
    parameter('provider',string(maxLength=40),description='With provider_id: the entry for one provider record, matched by the catalog record (tmdb, anilist, ...) or an external id (imdb, tvdb, ...).'),
    parameter('provider_id',string(maxLength=200),description='Identifier at provider; required with provider.'),
    parameter('list_id',ID,description='Requires lists:read as well. A smart list applies its stored rules; other filters are then ignored.')]
op('/api/v1/library','get','getLibrary','Browse your private library; token scope library:read','Library','EntryPage',params=PAGINATION+LIBRARY_FILTERS[:-1]+[
    parameter('sort',enum(*LIST_SORT['enum'], 'played'),description='added (default), activity, played, title, rating or year. played: the latest real play or finish, newest first; titles with neither come last. position applies to manual lists, whose own sort is the default.'),
    LIBRARY_FILTERS[-1]],
    description='Keyset-paginated in the chosen order; a cursor is valid only for the same filters and order.')
op('/api/v1/library/facets','get','getLibraryFacets','Count titles per media type and status for library filters; scope library:read','Library','LibraryFacets',params=LIBRARY_FILTERS,
    description='Takes the filters of getLibrary. Each media type is counted under every other filter but media_type, each status under every other filter but status, so a chip shows how many titles choosing it would give. Values without titles are left out.')
op('/api/v1/library','post','createEntry','Add private manual metadata; token scope library:write','Library','Mutation','EntryInput',201,idempotent=True,description='Manual entries are not published into a shared catalog. Status defaults to planned, progress to zero, notes to empty. Completing a known total fills progress. Idempotency acknowledgement does not imply current state on replay; refetch.')
op('/api/v1/library/{entryID}','get','getEntry','Read your item including private notes; scope library:read','Library','Entry')
op('/api/v1/library/{entryID}','put','replaceEntry','Replace your item metadata and tracking state; scope library:write','Library','Mutation','EntryInput',idempotent=True,description='Requires version and a valid status. Full replacement: omitted optional fields reset to empty/null/zero. Changing completed status back to another status corrects the current-cycle completion count. Use restart for a distinct repeat.')
op('/api/v1/library/{entryID}','delete','deleteEntry','Permanently delete your item, notes, activity and list memberships','Library','Mutation','VersionInput',idempotent=True,description='Scope library:write. Durable receipt retains only an intent hash and acknowledgement, never private notes. Replaying a previous acknowledgement cannot restore a deleted item.')
op('/api/v1/library/{entryID}/progress','post','updateProgress','Set absolute progress/status with version fencing; scope library:write','Library','Mutation','ProgressInput',idempotent=True,description='Absolute, not increment commands, prevent lost-response double progress. Known totals bound progress; completed fills total. Identical state does not create an extra activity event.')
op('/api/v1/library/{entryID}/restart','post','restartEntry','Start a distinct repeat without erasing prior activity; scope library:write','Library','Mutation','VersionInput',idempotent=True)
op('/api/v1/library-stats','get','getLibraryStats','Counts for your private library; scope library:read','Library','Stats')
op('/api/v1/activity','get','getActivity','Your activity timeline; scope library:read','Library','ActivityPage',params=PAGINATION+[parameter('entry_id',ID)],description='List cursor is not a synchronization cursor. Units reflect the event time; title reflects current metadata. Removing an entry removes its activity.')
schemas['CriticScore']=obj({'external_id':NINT, 'score':{'type':'number','nullable':True,'minimum':0,'maximum':100}, 'recommended':{'type':'number','nullable':True,'minimum':0,'maximum':100}, 'tier':string(), 'status':string(), 'checked_at':NDATE})
op('/api/v1/catalog/{catalogID}/critic','get','getCriticScore','Cached OpenCritic summary; schedules bounded background refresh; scope library:read','Catalog','CriticScore')
schemas.update({
    'QueryCapabilities': obj({'supported': {'type': 'boolean'}, 'pagination': {'type': 'boolean'}, 'year': {'type': 'boolean'}, 'genre': {'type': 'boolean'}, 'sorts': STRINGS}),
    'ProviderChoice': obj({'name': string(), 'kinds': {'type': 'array', 'items': TYPE}, 'defaults': {'type': 'array', 'items': TYPE}, 'configured': {'type': 'boolean'}, 'availability': enum('available', 'not_configured', 'temporarily_unavailable'), 'health': enum('unknown','available','not_configured','temporarily_unavailable'), 'search': ref('QueryCapabilities'), 'browse': ref('QueryCapabilities'), 'search_mode': enum('keyword','address')}),
    'ProviderChoicePage': items('ProviderChoice'),
})
catalog_options = [parameter('media_type', TYPE, required=True), parameter('provider', string(pattern='^[a-z0-9]{2,32}$'), description='Provider covering the media type; default otherwise.'), parameter('page', integer(minimum=1,maximum=500), description='Provider page token; keep the same query and filters. next_page is null at the last page.'), parameter('year', integer(minimum=1,maximum=9999)), parameter('genre', string(maxLength=100),description='Provider genre: a numeric TMDB genre ID or an AniList genre name.'), parameter('sort', enum('popular','rating','newest'))]
op('/api/v1/catalog/search','get','searchCatalog','Search provider metadata; scope library:read','Catalog','CatalogSearch',params=catalog_options+[parameter('q',string(minLength=1,maxLength=200),required=True,description='Up to 100 characters; public podcast feeds and YouTube addresses retain their existing lookup rules.')],description='Capabilities declare supported paging and filters. Unsupported controls return 422. Failed unfiltered first-page search uses the existing local shared catalog; filtered/paged outages return no mismatching fallback hits and availability explains the limitation. Membership is caller-private and attached after shared provider caching. 60 searches/browse requests per minute per account.')
op('/api/v1/catalog/browse','get','browseCatalog','Browse supported provider catalogs; scope library:read','Catalog','CatalogSearch',params=catalog_options,description='TMDB movie/series and AniList anime/manga provide paged browse with year/genre/sort. Search-only and missing-configuration adapters return an honest availability and no invented feed. Only use options declared by the selected provider.')
op('/api/v1/catalog/providers','get','getCatalogProviders','Expected and active provider capabilities; scope library:read','Catalog','ProviderChoicePage',description='Includes unconfigured adapters and registered extensions. Configuration and implemented capabilities are separate from live health; no setup setting names or credentials are returned to ordinary users. search_mode=address means URL/ID lookup, not keyword search.')
op('/api/v1/catalog/resolve','post','resolveCatalogItem','Fetch and store a provider record in the shared catalog','Catalog','CatalogItem','ResolveInput',auth=TOKEN,description='Requires library:read. Returns the stored record, refreshing it when older than seven days. During a provider outage stale metadata is returned. Shared catalog records never contain private data.')
op('/api/v1/catalog/{catalogID}','get','getCatalogItem','Shared metadata plus your own library entry, if tracked','Catalog','CatalogDetail')
op('/api/v1/catalog/{catalogID}/personal-state','get','getPersonalMemberships','Your favourites and watchlist membership for a movie or series; scope library:read','Catalog','PersonalMemberships')
op('/api/v1/catalog/{catalogID}/personal-state','put','setPersonalMembership','Add or remove your favourite or watchlist membership; scope library:write','Catalog',body='PersonalMembershipInput',status=204,idempotent=True,description='Independent of progress, status, notes and history. Removals are retained for synchronization; repeating an identical intent is safe.')
op('/api/v1/catalog/{catalogID}/children','get','getCatalogChildren','Seasons of a show, episodes of a season, issues or podcast episodes','Catalog','CatalogItemPage',description='A season\'s episodes load from the provider on first request. Natural order; at most 2,000 records.')
op('/api/v1/library/track','post','trackCatalogItem','Add a catalog record to your library; scope library:write','Library','Mutation','TrackInput',201,idempotent=True,description='Title, type, unit and total come from the catalog. 409 already_tracked when the record is already in your library.')
op('/api/v1/library/{entryID}/plays','get','getPlays','Plays in one repeat cycle of your item; scope library:read','Library','PlayPage',params=[parameter('cycle',integer(minimum=1),description='Defaults to the current cycle.')])
op('/api/v1/library/{entryID}/plays','post','addPlays','Record 1 to 500 plays of this item or its episodes; scope library:write','Library','Mutation','PlayInput',idempotent=True,description='Atomic. Each play targets the item\'s own record or a descendant. Show progress counts distinct regular-season episodes in the current cycle; specials are recorded but excluded. A play of a single work completes it. Requires the current version.')
op('/api/v1/library/{entryID}/plays/{playID}','delete','removePlay','Delete one mistaken play and recompute progress; scope library:write','Library','Mutation','VersionInput',idempotent=True)
schemas.update({
 'Checkin': obj({'id': ID, 'entry_id': ID, 'catalog_id': ID, 'kind': enum('play', 'session'), 'status': enum('active', 'finished', 'cancelled'), 'media_kind': string(maxLength=32), 'title': string(maxLength=500), 'item_title': string(maxLength=500, description='The episode, issue or track checked in to, when it is part of a title.'), 'season_number': integer(minimum=0), 'episode_number': integer(minimum=0), 'image_url': string(maxLength=1000), 'unit': string(maxLength=16), 'started_at': DATE, 'expected_end_at': {**DATE, 'description': 'Films and episodes only: when the check-in finishes on its own.'}, 'finished_at': DATE, 'minutes': integer(minimum=0, maximum=100000), 'amount': integer(minimum=0, maximum=1000000, description='Pages, chapters or other units read in a session.'), 'completed': {'type': 'boolean'}, 'details': {'type': 'object', 'additionalProperties': True, 'description': 'Board games: players and location. Games: platform.'}}, ['id', 'entry_id', 'catalog_id', 'kind', 'status', 'media_kind', 'title', 'image_url', 'unit', 'started_at', 'completed', 'details']),
 'CheckinPage': items('Checkin'),
 'CheckinInput': obj({'catalog_id': ID, 'started_at': {**DATE, 'description': 'Defaults to now; at most a week ago.'}}, ['catalog_id']),
 'BoardGamePlayer': obj({'name': string(minLength=1, maxLength=80), 'score': {'type': 'number'}, 'winner': {'type': 'boolean'}}, ['name']),
 'CheckinFinish': obj({'finished_at': {**DATE, 'description': 'Defaults to now.'}, 'minutes': integer(minimum=0, maximum=100000, description='Defaults to the time since the check-in started.'), 'amount': integer(minimum=0, maximum=1000000, description='Sessions only: pages, chapters or other units read.'), 'completed': {'type': 'boolean', 'description': 'Sessions only: the work is finished.'}, 'players': {'type': 'array', 'maxItems': 20, 'items': ref('BoardGamePlayer')}, 'location': string(maxLength=120), 'platform': string(maxLength=60)}, []),
})
schemas.update({
 'TitleHistoryItem': obj({'kind': enum('play', 'session'), 'play_id': {**NID, 'description': 'The play, for deleting it; null for a session.'}, 'catalog_id': {**ID, 'description': 'The record played or read: the title itself, or its episode or issue.'}, 'season_number': NINT, 'episode_number': NINT, 'at': DATE, 'minutes': integer(minimum=0), 'amount': integer(minimum=0), 'source': string(maxLength=32), 'item_title': string(maxLength=500), 'details': {'type': 'object', 'additionalProperties': True, 'description': 'Board games: players and location. Games: platform.'},
 'marked': {'type': 'boolean', 'description': 'A bulk "mark as played", listed but left out of the totals.'}}, ['kind', 'play_id', 'catalog_id', 'season_number', 'episode_number', 'at', 'source', 'details', 'marked']),
 'PlayerRecord': obj({'name': string(maxLength=80), 'plays': integer(minimum=0), 'wins': integer(minimum=0)}),
 'TitleHistory': obj({'items': {'type': 'array', 'maxItems': 500, 'items': ref('TitleHistoryItem')}, 'plays': integer(minimum=0), 'sessions': integer(minimum=0), 'minutes': integer(minimum=0), 'players': {'type': 'array', 'items': ref('PlayerRecord')}}),
})
op('/api/v1/library/{entryID}/play-history','get','getPlayHistory','Plays and finished sessions of your item, newest first, with totals; scope library:read','Library','TitleHistory',description='Totals cover every cycle; the list holds the newest 500. Board game players are counted by name, ignoring case.')
op('/api/v1/checkins','get','getCheckins','Your active check-ins, newest first; scope library:read','Library','CheckinPage')
op('/api/v1/checkins','post','checkIn','Check in to a title, episode, issue or track; scope library:write','Library','Checkin','CheckinInput',status=201,idempotent=True,description='Adds the title to your library when needed and resumes a planned, paused or dropped one. One active check-in per record. Films and episodes finish on their own at the end of their runtime; books, comics, manga, audiobooks and games are sessions.')
op('/api/v1/checkins/{checkinID}','get','getCheckin','One of your check-ins; scope library:read','Library','Checkin')
op('/api/v1/checkins/{checkinID}/finish','post','finishCheckin','Finish an active check-in; scope library:write','Library','Checkin','CheckinFinish',idempotent=True,description='A play check-in records a play with the time spent. A session records time and amount, moves progress on and completes the work only when it is done or marked completed. Players and location are for board games, platform for games.')
op('/api/v1/checkins/{checkinID}/cancel','post','cancelCheckin','Cancel an active check-in without recording anything; scope library:write','Library','Checkin',idempotent=True)
DATEQ = lambda name, text: parameter(name, string(maxLength=40), description=text)
op('/api/v1/home','get','getHome','Everything the home screen needs in one request; scope library:read','Insights','Home',description='Continue tracking (most recent activity), next unwatched aired episode of in-progress shows, releases in the next 14 days, planned, recently finished and counts.')
op('/api/v1/history','get','getHistory','Your play history, newest first; scope library:read','Insights','HistoryPage',params=PAGINATION+[DATEQ('from','Inclusive start, YYYY-MM-DD (UTC) or RFC 3339.'),DATEQ('to','Exclusive end.'),parameter('media_type',TYPE),parameter('entry_id',ID,description='Only plays of this library item.')],description='Keyset pagination on play time. Minutes are the played item\'s runtime when known, else null.')
op('/api/v1/history/{playID}','delete','deleteHistoryPlay','Delete one play from your history; scope library:write','Insights','Mutation',idempotent=True,description='Recomputes the item\'s progress. No version is needed because deleting a specific fact cannot overwrite another change.')
op('/api/v1/stats','get','getStats','Statistics for a time window; scope library:read','Insights','Insights',params=[DATEQ('from','Defaults to one year before to.'),DATEQ('to','Exclusive; defaults to tomorrow (UTC).')],description='At most ten years. Genres, people, studios and creators count each title once per window. Daily buckets use UTC dates. Time includes finished reading and game sessions; a session that finished the work counts once, as its play. The current streak ends today or yesterday; the longest streak lies inside the window.')
op('/api/v1/stats/years','get','getYearStats','Totals for every year with activity, oldest first; scope library:read','Insights','YearStatsPage',description='Calendar years in UTC.')
op('/api/v1/review/{year}','get','getYearReview','Your Year in Review; scope library:read','Insights','YearReview',description='Any year from 1900 up to the current one, in UTC. The current year is reviewed so far.')
op('/api/v1/calendar','get','getCalendar','Native and authorized source release dates; scope library:read','Insights','CalendarReleasePage',params=[DATEQ('from','Defaults to today (UTC).'),DATEQ('to','Inclusive; defaults to 30 days after from.'),CALENDAR_FILTER],description='Planned, in-progress and paused items only. At most 400 days and 1,000 releases. Episodes appear once their season is loaded; tracked shows load their newest seasons in the background.')
op('/api/v1/me/calendar-feed','get','getCalendarFeed','Whether your calendar subscription is enabled','Account','FeedStatus',auth=SESSION)
op('/api/v1/me/calendar-feed','post','rotateCalendarFeed','Create or replace your calendar subscription link','Account','FeedCreated',status=201,auth=SESSION,description='Replaces any previous link. The link grants read access to release dates of followed titles only. Password changes and access revocation remove it.')
op('/api/v1/me/calendar-feed','delete','revokeCalendarFeed','Disable your calendar subscription link','Account',status=204,auth=SESSION)
op('/calendar/{secret}.ics','get','getCalendarFeedDocument','iCalendar subscription document','Insights',auth=None,params=[CALENDAR_FILTER],description='For calendar applications. The path secret is the credential; 60 requests per minute per address. Covers 30 days back to 180 days ahead.')
paths['/calendar/{secret}.ics']['get']['responses']['200'] = {'description': 'iCalendar document', 'content': {'text/calendar': {'schema': string()}}}
op('/api/v1/connections','get','getConnections','Your media server connections; never their secrets','Connections','ConnectionPage',auth=SESSION)
op('/api/v1/connections','post','createConnection','Connect Plex, Jellyfin or Emby and get a webhook URL','Connections','ConnectionCreated','ConnectInput',201,SESSION,description='One connection per server type; creating it again replaces the URL. Completed viewings of movies and episodes are matched through TMDB (requires TMDB_API_KEY) and recorded as plays. Password changes and access revocation remove connections.')
op('/api/v1/connections/{connectionID}','patch','updateConnection','Change a connection\'s account filter','Connections','Connection','ConnectionUpdate',auth=SESSION)
op('/api/v1/connections/{connectionID}','delete','deleteConnection','Disconnect a media server; its webhook URL stops working','Connections',status=204,auth=SESSION)
op('/webhooks/{kind}/{secret}','post','receiveWebhook','Media server webhook delivery','Connections','WebhookResult',status=202,auth=None,params=[{'name': 'X-Event-ID', 'in': 'header', 'required': False, 'schema': string(maxLength=200), 'description': 'Stable provider delivery identity. Retries preserve the timestamp from the first delivery.'}],description='Plex sends multipart form data with a payload part; Jellyfin (webhook plugin), Emby, Kodi and Overseerr/Jellyseerr send JSON. Only completed viewings are queued (Plex media.scrobble, playback stop at completion, mark played, Kodi end or stop past 80%). The generic kind also accepts {media_type: movie|episode, tmdb_id, imdb_id, tvdb_id, show_tmdb_id, season, episode, completed or position_seconds and duration_seconds, watched_at}. The seerr kind adds requested movies and shows as planned; its account filter matches the requester. Ignored events also return 202 so servers do not retry them. Matching and recording happen in the worker. Supply a stable event_id in JSON or X-Event-ID header (at most 200 characters) for durable replay receipts. Distinct event IDs preserve rapid rewatches. Without an event ID, repeats within three hours are suppressed heuristically.')
for p in paths['/webhooks/{kind}/{secret}']['post']['parameters']:
    if p['name'] == 'kind': p['schema'] = enum('plex', 'jellyfin', 'emby', 'generic', 'seerr')
paths['/webhooks/{kind}/{secret}']['post']['requestBody'] = {'required': True, 'content': {'application/json': {'schema': {'type': 'object'}}, 'multipart/form-data': {'schema': {'type': 'object', 'properties': {'payload': string()}}}}}
COMPAT = [{'BearerToken': []}, {'CompatToken': []}]
COMPAT_NOTE = 'Compatibility adapter for the Silo/Bloem Floppy watch-provider plugin. Authenticate with a personal token holding library:read and library:write, as Bearer or Token. Not part of the native API; shapes follow that plugin.'
LOOSE = {'type': 'object', 'additionalProperties': True}
def compat(path, method, operation, title, params=None, body=False):
    v = {'operationId': operation, 'summary': title, 'tags': ['Floppy compatibility'], 'security': COMPAT, 'description': COMPAT_NOTE,
         'responses': {'200': {'description': 'Success', 'content': {'application/json': {'schema': LOOSE}}}, '401': response('Problem', 'Missing or invalid token'),
                       '403': response('Problem', 'Token lacks the scope'), '404': response('Problem', 'No matching title'), '422': response('Problem', 'Invalid request'),
                       '429': response('Problem', 'Rate limited'), '503': response('Problem', 'Metadata provider or service unavailable')}}
    if params: v['parameters'] = params
    if body: v['requestBody'] = {'required': True, 'content': {'application/json': {'schema': LOOSE}}}
    paths.setdefault(path, {})[method] = v
compat('/apis/listenbrainz/1/submit-listens', 'post', 'compatSubmitListens', 'Submit listens (single, import of up to 1000, or playing_now) as album plays', body=True)
paths['/apis/listenbrainz/1/submit-listens']['post']['description'] = 'ListenBrainz-compatible submission for scrobblers. Authenticate with a personal token holding library:write, as Token or Bearer. Listens are queued and recorded as plays of their album, matched by MusicBrainz release group, release, or exact album title and artist; a listen already recorded (including one pulled from a linked ListenBrainz account) is not repeated. playing_now is accepted and ignored.'
KOSYNC_HEADERS = [parameter('x-auth-user', string(), 'header', True, 'Your username.'), parameter('x-auth-key', string(), 'header', True, 'MD5 of your KOReader password, as hex; KOReader sends this itself.')]
def kosync(path, method, operation, title, description, body=None, params=None, auth=True):
    v = {'operationId': operation, 'summary': title, 'tags': ['KOReader'], 'security': [], 'description': description,
         'responses': {'200': {'description': 'Success', 'content': {'application/json': {'schema': LOOSE}}}, '429': response('Problem', 'Rate limited')}}
    v['parameters'] = (KOSYNC_HEADERS if auth else []) + (params or [])
    if auth: v['responses']['401'] = {'description': 'Unknown user or wrong key', 'content': {'application/json': {'schema': LOOSE}}}
    if body: v['requestBody'] = {'required': True, 'content': {'application/json': {'schema': body}}}
    paths.setdefault(path, {})[method] = v
KOSYNC_NOTE = 'KOReader progress sync protocol. In KOReader open Tools → Progress sync → Custom sync server, enter this server address followed by /kosync, and sign in with your username and the KOReader password set under Integrations.'
kosync('/kosync/users/auth', 'get', 'kosyncAuth', 'Check KOReader credentials', KOSYNC_NOTE)
kosync('/kosync/users/create', 'post', 'kosyncRegister', 'Registration from KOReader (always refused)', 'Accounts are never created from a device; answers 402 with a pointer to Integrations.', auth=False)
paths['/kosync/users/create']['post']['responses'] = {'402': {'description': 'Registration happens in the application', 'content': {'application/json': {'schema': LOOSE}}}}
kosync('/kosync/syncs/progress', 'put', 'kosyncPush', 'Store a document position', KOSYNC_NOTE + ' A document linked to a title moves it forward; reaching the end completes it.',
       body=obj({'document': string(maxLength=128), 'progress': string(maxLength=500), 'percentage': {'type': 'number', 'minimum': 0, 'maximum': 1}, 'device': string(maxLength=100), 'device_id': string(maxLength=100)}, ['document', 'percentage']))
paths['/kosync/syncs/progress']['put']['responses']['400'] = {'description': 'Invalid document', 'content': {'application/json': {'schema': LOOSE}}}
kosync('/kosync/syncs/progress/{document}', 'get', 'kosyncPull', 'Latest position of a document', 'An unknown document returns an empty object.', params=[parameter('document', string(maxLength=128), 'path', True)])
kosync('/kosync/healthcheck', 'get', 'kosyncHealth', 'KOReader sync health check', 'Always {"state":"OK"}.', auth=False)
schemas.update({
    'KOReaderDocument': obj({'document': string(), 'percentage': {'type': 'number'}, 'device': string(), 'entry_id': {**ID, 'nullable': True}, 'entry_title': string(nullable=True), 'updated_at': DATE}),
    'KOReaderStatus': obj({'configured': {'type': 'boolean'}, 'username': string(), 'documents': array('KOReaderDocument')}),
    'KOReaderPassword': obj({'password': string(minLength=8, maxLength=200)}),
    'KOReaderLink': obj({'entry_id': {**ID, 'nullable': True}}),
})
op('/api/v1/me/koreader','get','getKOReader','KOReader sync status and synced documents','Imports','KOReaderStatus',auth=SESSION)
op('/api/v1/me/koreader','put','setKOReaderPassword','Set the password KOReader signs in with','Imports','KOReaderStatus','KOReaderPassword',auth=SESSION,description='Separate from your account password, because KOReader stores it with weak hashing. Setting it again replaces it.')
op('/api/v1/me/koreader','delete','disableKOReader','Stop KOReader sync','Imports',status=204,auth=SESSION,description='Devices can no longer sign in. Synced positions stay until you set a password again.')
op('/api/v1/me/koreader/documents/{document}','put','linkKOReaderDocument','Link a synced document to a title, or unlink it with null','Imports','KOReaderStatus','KOReaderLink',auth=SESSION,description='The latest position is applied right away.')
for p_ in paths['/api/v1/me/koreader/documents/{document}']['put']['parameters']:
    if p_['name'] == 'document': p_['schema'] = string(maxLength=128)
PAGE = [parameter('limit', integer(minimum=1, maximum=200, default=50)), parameter('offset', integer(minimum=0, default=0))]
compat('/apis/listenbrainz/1/validate-token', 'get', 'compatValidateToken', 'Check a personal token (valid is false for a missing, revoked or under-scoped token)')
paths['/apis/listenbrainz/1/validate-token']['get']['security'] = []
compat('/api/v1/history/', 'get', 'compatHistory', 'Plays grouped by UTC day with offset pagination', PAGE + [parameter('start_date', string(pattern='^[0-9]{4}-[0-9]{2}-[0-9]{2}$')), parameter('end_date', string(pattern='^[0-9]{4}-[0-9]{2}-[0-9]{2}$'), description='Inclusive.'), parameter('logging_style', string(), description='Accepted; every play is its own entry.')])
compat('/api/v1/playback/progress/', 'get', 'compatProgress', 'Unfinished resume positions for movies and episodes', PAGE + [parameter('media_type', string(), description='Comma-separated: movie, episode.'), parameter('completed', string()), parameter('updated_since', string(format='date-time'))])
compat('/api/v1/scrobble/', 'post', 'compatScrobble', 'Start, pause or stop playback; a completed stop records a play', body=True)
op('/api/v1/imports','post','startImport','Import from Trakt, Letterboxd, IMDb, Goodreads, StoryGraph, AniList, Floppy or Yamtrack, Grouvee, TV Time, CLZ, any CSV or a native export; scope library:write','Imports','ImportRun',status=202,description='Multipart form data with source and file (up to 64 MiB), or JSON {source:"anilist", username} for a public AniList profile. One import runs at a time per account (409 import_in_progress). Processing is durable and resumable; imports only add or upgrade (status never moves backwards, existing ratings and notes are kept) and every play carries the source identity, so running the same import again adds nothing. Titles are matched by identifiers first, then by exact title and year; unmatched titles are listed, never guessed. Failed and partial_success runs retain their upload and failed records for repair; completed and cancelled runs release the upload. Native and WeTrakr imports also require lists:write. Native version 2 or 3 JSONL (optionally gzip) restores into an empty library atomically, including manual entries, cycles, plays, copies, fields, tags, lists, saved views, rating timestamps, episode ratings, status history and deletion markers. Restored lists are private. Credentials, sharing grants and linked-service configuration are excluded. Catalog references must resolve through normal providers. A manifest and SHA-256 trailer reject incomplete files; exact file retries are no-ops. Native restores do not merge into nonempty libraries. Limits are 64 MiB uncompressed and 100,000 records. The csv source reads any CSV with recognizable columns: title or name, type, year, status, rating (0-10) or stars (0-5), date, tmdb_id, imdb_id, isbn, author, season, episode and notes. The clz source reads a CLZ collection CSV: titles are added as planned with an owned copy each (format, price, store, purchase date), and any other filled column becomes a custom collection field.')
paths['/api/v1/imports']['post']['requestBody'] = {'required': True, 'content': {
    'multipart/form-data': {'schema': {'type': 'object', 'required': ['source', 'file'], 'properties': {'source': enum(*[v for v in FILE_SOURCES if v != 'anilist']), 'file': string(description='The export file (.zip or .csv).')}}},
    'application/json': {'schema': obj({'source': enum('anilist'), 'username': string(minLength=1, maxLength=60)})}}}
paths['/api/v1/imports']['post']['responses']['415'] = response('Problem', 'Multipart or JSON required')
schemas.update({
    'Unmatched': obj({'id': ID, 'run_id': string(format='uuid'), 'source': string(), 'label': string(), 'kind': string(), 'year': NINT, 'plays': integer(minimum=0),
                      'status': enum('pending', 'resolved', 'ignored'), 'catalog_id': NID, 'created_at': DATE}),
    'UnmatchedPage': paged('Unmatched'),
    'ResolveUnmatchedInput': obj({'catalog_id': ID}),
    'RollbackResult': obj({'import': ref('ImportRun'), 'entries_removed': integer(minimum=0), 'plays_removed': integer(minimum=0)}),
})
op('/api/v1/import-unmatched','get','getUnmatched','Titles your imports could not match; scope library:read','Imports','UnmatchedPage',params=PAGINATION+[parameter('run_id',string(format='uuid')),parameter('status',enum('pending','resolved','ignored'),description='Defaults to pending.')])
op('/api/v1/import-unmatched/{unmatchedID}/resolve','post','resolveUnmatched','Match an unmatched title by hand and apply it; scope library:write','Imports','Unmatched','ResolveUnmatchedInput',description='Applies the stored title exactly as the import would have, merging forward. Search the catalog first to find the catalog_id. 409 already_handled when it was matched or ignored before.')
op('/api/v1/import-unmatched/{unmatchedID}/ignore','post','ignoreUnmatched','Dismiss an unmatched title; scope library:write','Imports','Unmatched')
op('/api/v1/imports/{importID}/rollback','post','rollbackImport','Undo an import; scope library:write','Imports','RollbackResult',description='Removes the plays the run added, and the titles it created unless you have acted on them since. Status or rating upgrades it made to titles you already had are kept. Pending unmatched titles from the run are dismissed.')
op('/api/v1/imports','get','getImports','Your 50 most recent imports; scope library:read','Imports','ImportPage')
op('/api/v1/imports/{importID}','get','getImport','One import with progress and unmatched titles; scope library:read','Imports','ImportRun')
op('/api/v1/imports/{importID}/cancel','post','cancelImport','Stop an import after its current title; scope library:write','Imports','ImportRun',description='Titles already applied stay applied.')
for path, operation, title, media in [('/api/v1/export/library.jsonl','exportLibraryNative','Full export as JSON lines, re-importable here','application/x-ndjson'),
                                     ('/api/v1/export/library.csv','exportLibraryCSV','Library as CSV for spreadsheets','text/csv'),
                                     ('/api/v1/export/letterboxd.csv','exportLetterboxd','Watched movies in Letterboxd import format','text/csv')]:
    op(path,'get',operation,title+'; scope library:read','Imports',description='Streams from one consistent snapshot. Ten exports per minute per account. Native JSONL additionally requires lists:read and uses version 3 with a source namespace, individual records and a completeness trailer; maximum 64 MiB and 100,000 records. Restore targets an empty library and keeps lists private.')
    paths[path]['get']['responses']['200'] = {'description': 'Download', 'content': {media: {'schema': string()}}}
TAG = obj({'id': ID, 'name': string(), 'color': string(), 'entries': integer(minimum=0)})
schemas.update({
    'Tag': TAG,
    'TagPage': items('Tag'),
    'TagInput': obj({'name': string(minLength=1, maxLength=50), 'color': string(pattern='^(#[0-9a-fA-F]{6})?$')}, ['name']),
    'TagIDsInput': obj({'tag_ids': {'type': 'array', 'items': ID, 'maxItems': 50}}),
    'BulkInput': obj({'entry_ids': {'type': 'array', 'items': ID, 'minItems': 1, 'maxItems': 200}, 'action': enum('add_tags', 'remove_tags', 'set_status', 'add_to_list', 'remove_from_list', 'delete'),
                      'tag_ids': {'type': 'array', 'items': ID, 'maxItems': 20}, 'status': STATUS, 'list_id': ID}, ['entry_ids', 'action']),
    'BulkResult': obj({'changed': integer(minimum=0), 'replayed': {'type': 'boolean'}}),
    'Copy': obj({'id': ID, 'entry_id': ID, 'entry_title': string(), 'media_type': TYPE, 'image_url': string(), 'catalog_item_id': NID, 'scope_title': string(),
                 'format': COPY_FORMAT, 'edition': string(), 'resolution': string(), 'hdr': string(), 'audio': string(), 'bitrate_kbps': NINT,
                 'acquired_on': string(nullable=True, pattern='^[0-9]{4}-[0-9]{2}-[0-9]{2}$'), 'price_cents': NINT, 'currency': string(), 'store': string(), 'location': string(),
                 'notes': string(), 'custom': {'type': 'object', 'additionalProperties': True}, 'source': string(), 'version': VERSION, 'created_at': DATE, 'updated_at': DATE}),
    'CopyPage': obj({'items': array('Copy'), 'next_cursor': string(nullable=True), 'total': integer(), 'titles': integer(), 'spent': {'type': 'object', 'additionalProperties': integer()}, 'formats': {'type': 'object', 'additionalProperties': integer()}}),
    'CopyInput': obj({'catalog_item_id': {**ID, 'nullable': True, 'description': 'A season or volume of the entry\'s title that this copy covers; null for the whole title.'},
                      'format': COPY_FORMAT, 'edition': string(maxLength=200), 'resolution': enum('', 'sd', '720p', '1080p', '2160p', '4320p'), 'hdr': enum('', 'hdr10', 'hdr10plus', 'dolby_vision', 'hlg'),
                      'audio': string(maxLength=100), 'bitrate_kbps': integer(nullable=True, minimum=1, maximum=1000000), 'acquired_on': string(nullable=True, pattern='^[0-9]{4}-[0-9]{2}-[0-9]{2}$'),
                      'price_cents': integer(nullable=True, minimum=0, maximum=100000000), 'currency': string(pattern='^([A-Za-z]{3})?$'), 'store': string(maxLength=200), 'location': string(maxLength=200),
                      'notes': string(maxLength=2000), 'custom': {'type': 'object', 'additionalProperties': True, 'maxProperties': 50}, 'version': VERSION}, ['format']),
    'Field': obj({'id': ID, 'name': string(), 'kind': FIELD_KIND, 'choices': STRINGS, 'position': integer(minimum=0)}),
    'FieldPage': items('Field'),
    'FieldInput': obj({'name': string(minLength=1, maxLength=60), 'kind': FIELD_KIND, 'choices': {'type': 'array', 'items': string(maxLength=100), 'maxItems': 50}, 'position': integer(minimum=0, maximum=1000)}, ['name', 'kind']),
    'HomeRow': obj({'kind': enum('up_next', 'continue', 'upcoming', 'planned', 'recently_finished', 'list', 'mix'), 'list_id': ID, 'visible': {'type': 'boolean'},
                    'id': string(pattern='^[a-z0-9]{1,16}$', description='Mix rows only: a stable row identifier.'), 'title': string(minLength=1, maxLength=60, description='Mix rows only.'),
                    'media_types': {'type': 'array', 'items': TYPE, 'minItems': 1, 'description': 'Mix rows only: titles of any of these types.'}, 'statuses': {'type': 'array', 'items': STATUS, 'maxItems': 6, 'description': 'Mix rows only: optional status filter.'},
                    'sort': {**enum('added', 'activity', 'title', 'rating', 'year'), 'description': 'Mix rows only; default activity.'}, 'limit': integer(minimum=1, maximum=30, description='Mix rows only; default 20.')}, ['kind', 'visible']),
    'Preferences': obj({'home_rows': {'type': 'array', 'items': ref('HomeRow'), 'maxItems': 20}, 'release_region': string(pattern='^[A-Za-z]{2}$', description='Two-letter country for movie release dates in the calendar; empty keeps primary dates.'), 'calendar_release_types': {'type': 'array', 'items': enum('theatrical', 'digital', 'physical'), 'maxItems': 3, 'uniqueItems': True, 'description': 'Movie release types shown for release_region.'}, 'card_fields': {'type': 'object', 'additionalProperties': {'type': 'array', 'items': enum('type', 'year', 'status', 'progress', 'rating'), 'maxItems': 5}, 'description': 'Details shown under posters, per media type; a missing type uses the default.'}, 'rating_scale': enum('ten', 'five', 'hundred', 'off'), 'title_language': string(pattern='^(localized|original|[a-z]{2})$', description='localized (the server metadata language), original, or a two-letter language code such as de.'),
                        'progress_display': enum('count', 'percent'), 'library_view': enum('grid', 'list'), 'library_sort': enum('added', 'title', 'rating', 'year', 'activity'),
                        'week_start': enum('monday', 'sunday'), 'language': {**enum('', 'en', 'de', 'fr', 'nl', 'es'), 'description': 'Interface language; empty follows the browser.'}, 'anime_shape': {**enum('flat', 'grouped'), 'description': 'flat finds anime season by season (AniList); grouped finds whole shows with seasons (TMDB).'}, 'public_profile': {'type': 'boolean'}, 'decline_recommendations': {'type': 'boolean'}, 'onboarded': {'type': 'boolean'}, 'version': integer(minimum=0, description='0 until preferences are first saved.')}, ['home_rows', 'rating_scale', 'title_language', 'progress_display', 'library_view', 'library_sort', 'week_start', 'public_profile', 'version']),
    'SkipInput': obj({'version': VERSION, 'catalog_ids': {'type': 'array', 'items': ID, 'minItems': 1, 'maxItems': 500}, 'skip': {'type': 'boolean'}}),
    'IDPage': obj({'items': {'type': 'array', 'items': ID}}),
    'ReorderInput': obj({'version': VERSION, 'entry_ids': {'type': 'array', 'items': ID, 'minItems': 1, 'maxItems': 5000}}),
    'ListNoteInput': obj({'version': VERSION, 'note': string(maxLength=500)}),
    'PublicItem': obj({'title': string(), 'media_type': TYPE, 'year': NINT, 'image_url': string(), 'provider': string(), 'provider_id': string(),
                       'external_ids': {'type': 'object', 'additionalProperties': True, 'nullable': True}, 'note': string(), 'catalog_id': NID}),
    'PublicList': obj({'owner': string(), 'owner_name': string(), 'name': string(), 'description': string(), 'slug': string(), 'kind': enum('manual', 'smart'), 'updated_at': DATE, 'items': array('PublicItem'), 'next_cursor': string(nullable=True)}),
    'PublicProfile': obj({'username': string(), 'display_name': string(), 'items': array('List')}),
})
op('/api/v1/tags','get','getTags','Your tags with how many titles carry each; scope library:read','Library','TagPage',description='Up to 500, alphabetical.')
op('/api/v1/tags','post','createTag','Create a tag; scope library:write','Library','Mutation','TagInput',201,idempotent=True,description='Names are unique per account, ignoring case (409 duplicate_tag).')
op('/api/v1/tags/{tagID}','put','updateTag','Rename or recolour a tag; scope library:write','Library','Mutation','TagInput',idempotent=True)
op('/api/v1/tags/{tagID}','delete','deleteTag','Delete a tag and remove it from every title; scope library:write','Library','Mutation',idempotent=True)
op('/api/v1/library/{entryID}/tags','get','getEntryTags','Tags on one of your titles; scope library:read','Library','TagPage')
op('/api/v1/library/{entryID}/tags','put','setEntryTags','Set exactly which tags a title carries; scope library:write','Library','TagPage','TagIDsInput',idempotent=True)
op('/api/v1/library/bulk','post','bulkLibrary','Apply one action to up to 200 of your titles','Library','BulkResult','BulkInput',idempotent=True,description='Actions: add_tags, remove_tags, set_status, add_to_list, remove_from_list, delete. Atomic: one missing or foreign id fails the batch (404). List actions need lists:write; the others library:write. changed counts titles whose state actually changed.')
op('/api/v1/library/{entryID}/copies','get','getCopies','Owned copies of one title; scope library:read','Collection','CopyPage',params=PAGINATION)
op('/api/v1/library/{entryID}/copies','post','createCopy','Record an owned copy; scope library:write','Collection','Mutation','CopyInput',201,idempotent=True,description='At most 100 copies per title. A copy can cover one season or volume of the title.')
op('/api/v1/library/{entryID}/copies/{copyID}','put','replaceCopy','Replace an owned copy; scope library:write','Collection','Mutation','CopyInput',idempotent=True,description='Requires the current copy version.')
op('/api/v1/library/{entryID}/copies/{copyID}','delete','deleteCopy','Remove an owned copy; scope library:write','Collection','Mutation',idempotent=True)
op('/api/v1/collection','get','getCollection','Everything you own, newest first; scope library:read','Collection','CopyPage',params=PAGINATION+[parameter('format',COPY_FORMAT)],description='Cursor-paginated copies. Total copies, distinct titles and per-currency spending cover the complete filtered collection; format counts cover all formats.')
op('/api/v1/collection-fields','get','getCollectionFields','Your custom fields for owned copies; scope library:read','Collection','FieldPage')
op('/api/v1/collection-fields','post','createCollectionField','Add a custom field; scope library:write','Collection','Mutation','FieldInput',201,idempotent=True)
op('/api/v1/collection-fields/{fieldID}','put','updateCollectionField','Change a custom field; scope library:write','Collection','Mutation','FieldInput',idempotent=True)
op('/api/v1/collection-fields/{fieldID}','delete','deleteCollectionField','Delete a custom field; scope library:write','Collection','Mutation',idempotent=True,description='Values already stored on copies are kept under their key.')
op('/api/v1/library/{entryID}/skipped','get','getSkipped','Episodes you skipped in a cycle; scope library:read','Library','IDPage',params=[parameter('cycle',integer(minimum=1),description='Defaults to the current cycle.')])
op('/api/v1/library/{entryID}/skips','post','setSkipped','Skip or unskip episodes of a title; scope library:write','Library','Mutation','SkipInput',idempotent=True,description='A skipped episode counts toward completion and moves up next past it, but is never recorded as a play. Requires the current entry version.')
op('/api/v1/me/preferences','get','getPreferences','Your display preferences and home layout','Account','Preferences',auth=SESSION)
op('/api/v1/me/preferences','put','savePreferences','Replace your display preferences and home layout','Account','Preferences','Preferences',auth=SESSION,description='Send the version you read; 0 creates them. A stale version returns 409. public_profile makes your public lists browsable at /api/v1/public/{username}/lists.')
schemas.update({
    'Tier': obj({'id': ID, 'label': string(), 'color': {**string(pattern='^#[0-9a-f]{6}$'), 'nullable': True}, 'entry_ids': {'type': 'array', 'items': ID}}),
    'TierBoard': obj({'version': VERSION, 'tiers': array('Tier'), 'unranked': {'type': 'array', 'items': ID}}),
    'TierInputTier': obj({'label': string(minLength=1, maxLength=40), 'color': {**string(pattern='^#[0-9a-fA-F]{6}$'), 'nullable': True}, 'entry_ids': {'type': 'array', 'items': ID, 'maxItems': 5000}}, ['label', 'entry_ids']),
    'TierInput': obj({'version': VERSION, 'tiers': {'type': 'array', 'items': ref('TierInputTier'), 'maxItems': 20}}),
})
op('/api/v1/lists/{listID}/tiers','get','getListTiers','Read a manual list as a tier board; scope lists:read','Lists','TierBoard',description='Members in no tier are unranked, in the list order. Smart lists have no board.')
op('/api/v1/lists/{listID}/tiers','put','saveListTiers','Replace a manual list tier board; scope lists:write','Lists','Mutation','TierInput',idempotent=True,description='Names every tier in order with its members in order; members not named become unranked. Each member appears at most once. The list manual order is unchanged.')
op('/api/v1/lists/{listID}/order','put','reorderList','Set the manual order of a list; scope lists:write','Lists','Mutation','ReorderInput',idempotent=True,description='Move the named members to the front in the supplied order. Unnamed members retain their relative order. Include at most 5000 distinct existing entry IDs.')
op('/api/v1/lists/{listID}/entries/{entryID}/note','put','setListNote','Annotate a title within a list; scope lists:write','Lists','Mutation','ListNoteInput',idempotent=True,description='Notes appear on public and unlisted lists.')
op('/api/v1/public/{username}/lists','get','getPublicProfile','Public lists of an account that opted in','Public lists','PublicProfile',auth=None,description='Unlisted lists are not included. 60 requests per minute per address.')
op('/api/v1/public/{username}/lists/{slug}','get','getPublicList','A public or unlisted list','Public lists','PublicList',auth=None,params=PAGINATION,description='Unlisted access requires the owner-generated share_token in the slug path, valid for 90 days. Private visibility revokes it. Cursor-paginated public metadata and explicit list annotations; no private entry notes, ratings, progress or history. 60 requests per minute per address.')
for ext, media, label in [('rss', 'application/rss+xml', 'RSS feed'), ('csv', 'text/csv', 'CSV download')]:
    path = '/lists/{username}/{slug}.' + ext
    op(path,'get','getPublicList' + ext.upper(),'A public or unlisted list as ' + label,'Public lists',auth=None,params=PAGINATION,description='Same paginated content as JSON. Follow the Link response header with rel=next for subsequent pages.')
    paths[path]['get']['responses']['200'] = {'description': label, 'content': {media: {'schema': string()}}}
schemas.update({
    'ArtworkInput': obj({'version': VERSION, 'image_url': string(maxLength=1000, pattern='^(https://.*)?$', description='An https:// image; empty restores the catalog artwork.')}),
    'MissingEpisode': obj({'season': NINT, 'episode': NINT, 'title': string()}),
    'RemapPreview': obj({'version': VERSION, 'plays_moved': integer(minimum=0), 'plays_retained': integer(minimum=0), 'ratings_moved': integer(minimum=0, description='Episode ratings that follow their episodes.'), 'ratings_dropped': integer(minimum=0, description='Episode ratings with no counterpart, removed by the remap.'), 'missing': {'type': 'array', 'items': ref('MissingEpisode'), 'maxItems': 50, 'description': 'Tracked or rated episodes the new record lacks. The title rating, notes and status always stay.'}}),
    'RemapInput': obj({'version': VERSION, 'catalog_id': ID}),
    'RemapResult': obj({'id': ID, 'version': VERSION, 'replayed': {'type': 'boolean'}, 'deleted': {'type': 'boolean'}, 'plays_moved': integer(minimum=0), 'plays_dropped': integer(minimum=0, description='Legacy field; retained history is never dropped.'), 'plays_retained': integer(minimum=0)}),
})
op('/api/v1/library/{entryID}/artwork','put','setArtwork','Replace the cover shown for your title; scope library:write','Library','Mutation','ArtworkInput',idempotent=True,description='Personal only: shared lists always show catalog artwork.')
op('/api/v1/library/{entryID}/remap/preview','post','previewRemap','Preview a catalog remap without changing the library; scope library:write','Library','RemapPreview','RemapInput',description='Use the returned version when applying the remap. Unmatched plays are retained in history.')
op('/api/v1/library/{entryID}/remap','post','remapEntry','Point your title at a different catalog record of the same kind; scope library:write','Library','RemapResult','RemapInput',idempotent=True,description='For a wrong match or another edition. Plays, skips and copy scopes follow by season and episode number; plays with no unique counterpart retain their original catalog identity and remain in history, excluded from current progress. 409 already_tracked when the target is already in your library.')
NOTIFY_KIND = enum('webhook', 'ntfy', 'discord', 'gotify', 'apprise')
schemas.update({
    'NotificationTarget': obj({'id': ID, 'kind': NOTIFY_KIND, 'name': string(), 'description': string(description='Redacted destination: host and topic only.'), 'enabled': {'type': 'boolean'},
                               'last_status': string(), 'last_sent_at': {**DATE, 'nullable': True}, 'created_at': DATE}),
    'NotificationTargetPage': items('NotificationTarget'),
    'NotificationConfig': obj({'url': string(maxLength=1000, description='Webhook, Discord or Apprise address; ntfy or Gotify server.'), 'topic': string(maxLength=64, description='ntfy topic.'),
                               'token': string(maxLength=500, writeOnly=True, description='ntfy access token or Gotify application token.'), 'secret': string(maxLength=500, writeOnly=True, description='Webhook signing secret: requests carry X-Pastime-Signature: sha256=<hex HMAC of the body>.')}, []),
    'NotificationTargetInput': obj({'kind': NOTIFY_KIND, 'name': string(minLength=1, maxLength=80), 'enabled': {'type': 'boolean'}, 'config': ref('NotificationConfig')}, ['kind', 'name', 'config']),
    'NotificationSettings': obj({'releases': {'type': 'boolean'}, 'days_ahead': integer(minimum=0, maximum=14), 'media_types': {'type': 'array', 'items': TYPE, 'maxItems': 11},
                                 'statuses': {'type': 'array', 'items': enum('planned', 'in_progress', 'paused', 'completed', 'dropped'), 'maxItems': 5}, 'digest': {'type': 'boolean'}, 'version': integer(minimum=0)}),
    'ExclusionState': obj({'excluded': {'type': 'boolean'}}),
})
op('/api/v1/me/notification-targets','get','getNotificationTargets','Where your release notices are sent','Notifications','NotificationTargetPage',auth=SESSION)
op('/api/v1/me/notification-targets','post','createNotificationTarget','Add a notification destination','Notifications','NotificationTarget','NotificationTargetInput',201,SESSION,description='Destination details are encrypted with the server SECRET_KEY and never returned (503 secret_key_missing without it). At most 10. Private network addresses are refused unless the server allows them.')
op('/api/v1/me/notification-targets/{targetID}','put','replaceNotificationTarget','Replace a notification destination','Notifications','NotificationTarget','NotificationTargetInput',auth=SESSION,description='Send the full configuration again; it is never read back.')
op('/api/v1/me/notification-targets/{targetID}','delete','deleteNotificationTarget','Remove a notification destination','Notifications',status=204,auth=SESSION)
op('/api/v1/me/notification-targets/{targetID}/test','post','testNotificationTarget','Send a test notice','Notifications','NotificationTarget',auth=SESSION,description='Ten per minute. 502 delivery_failed carries the reason the destination gave.')
paths['/api/v1/me/notification-targets/{targetID}/test']['post']['responses']['502'] = response('Problem', 'The destination rejected or could not receive the notice')
op('/api/v1/me/notification-settings','get','getNotificationSettings','When you get release notices','Notifications','NotificationSettings',auth=SESSION)
op('/api/v1/me/notification-settings','put','saveNotificationSettings','Change when you get release notices','Notifications','NotificationSettings','NotificationSettings',auth=SESSION,description='Send the version you read; 0 creates them. Notices cover releases days_ahead days from today (UTC), for titles with the chosen statuses and types. Each release is announced once, however many of your titles point at it.')
op('/api/v1/library/{entryID}/notifications','get','getEntryNotifications','Whether release notices are muted for this title; scope library:read','Notifications','ExclusionState')
op('/api/v1/library/{entryID}/notifications','put','setEntryNotifications','Mute or unmute release notices for this title; scope library:write','Notifications','ExclusionState','ExclusionState')
schemas.update({
    'AuthMethods': obj({'registration': {'type':'boolean'}, 'password': {'type': 'boolean'}, 'oidc': obj({'enabled': {'type': 'boolean'}, 'name': string(), 'start_url': string()}), 'recovery': {'type': 'boolean', 'description': 'Authenticator password recovery is available.'}}),
    'OIDCIdentity': obj({'issuer': string(), 'email': string(), 'created_at': DATE, 'last_login_at': {**DATE, 'nullable': True}}),
    'OIDCIdentityPage': items('OIDCIdentity'),
    'RedirectURL': obj({'url': string()}),
    'RecoveryInput': obj({'username': string(maxLength=48), 'code': string(maxLength=40, writeOnly=True, description='A six-digit authenticator code or a recovery code.'), 'password': PASSWORD}),
    'PasswordConfirm': obj({'password': string(format='password', writeOnly=True, maxLength=128)}),
    'TOTPStatus': obj({'enabled': {'type': 'boolean'}, 'pending': {'type': 'boolean'}, 'confirmed_at': {**DATE, 'nullable': True}, 'recovery_remaining': integer(minimum=0)}),
    'TOTPSetup': obj({'secret': string(description='Base32 secret; shown once.'), 'otpauth_uri': string(description='Scan as a QR code in an authenticator app.')}),
    'TOTPConfirm': obj({'code': string(minLength=6, maxLength=10)}),
    'RecoveryCodes': obj({'recovery_codes': STRINGS}),
})
op('/api/v1/auth/methods','get','getAuthMethods','How this server lets people sign in','Account','AuthMethods',auth=None)
op('/auth/oidc/start','get','startOIDC','Begin single sign-on (browser redirect)','Account',auth=None,status=302,params=[parameter('return_to',string(maxLength=500),description='Path inside the application to open afterwards.')],description='Redirects to the provider with state, nonce and PKCE. Thirty starts per minute per address.')
op('/auth/oidc/callback','get','finishOIDC','Provider redirect target (browser only)','Account',auth=None,status=302,params=[parameter('code',string()),parameter('state',string()),parameter('error',string())],description='Verifies the ID token (issuer, audience, nonce) and signs in the linked account. Unlinked identities are refused unless the server creates accounts automatically. Failures redirect to /login?error=<code>.')
op('/api/v1/me/oidc','get','getOIDCIdentities','Single sign-on accounts linked to yours','Account','OIDCIdentityPage',auth=SESSION)
op('/api/v1/me/oidc','delete','unlinkOIDC','Unlink a single sign-on account','Account',status=204,auth=SESSION,params=[parameter('issuer',string(maxLength=500),required=True)],description='Refused when it is your only way to sign in.')
op('/api/v1/me/oidc/link','post','linkOIDC','Get the address that links a single sign-on account to yours','Account','RedirectURL',auth=SESSION,description='Open the returned URL; after signing in at the provider you return to your account page.')
schemas.update({
    'DeviceStartInput': obj({'client_name': string(minLength=1, maxLength=60), 'scopes': {'type': 'array', 'items': enum('profile:read', 'library:read', 'library:write', 'lists:read', 'lists:write'), 'maxItems': 5, 'uniqueItems': True}}, ['client_name']),
    'DeviceStart': obj({'device_code': string(), 'user_code': string(pattern='^[A-Z]{4}-[A-Z]{4}$'), 'expires_in': integer(), 'interval': integer(), 'verification_uri': string(), 'verification_uri_complete': string()}),
    'DevicePollInput': obj({'device_code': string(maxLength=100)}),
    'DevicePoll': obj({'status': enum('pending', 'slow_down', 'approved', 'denied', 'expired'), 'interval': integer(), 'access_token': string(description='Present once, with status approved.'), 'scopes': STRINGS}, ['status', 'interval']),
    'DeviceRequest': obj({'client_name': string(), 'scopes': STRINGS, 'expires_at': DATE}),
    'DeviceDecision': obj({'code': string(maxLength=20), 'approve': {'type': 'boolean'}}),
})
op('/api/v1/device/authorize','post','startDeviceSignIn','Start signing in an app on a TV or phone','Account','DeviceStart','DeviceStartInput',auth=None,description='Show user_code and verification_uri to the person, then poll /api/v1/device/token every interval seconds. Codes expire after ten minutes. Scopes default to every member scope. Ten per minute per address.')
op('/api/v1/device/token','post','pollDeviceSignIn','Poll a device sign-in request','Account','DevicePoll','DevicePollInput',auth=None,description='pending: keep polling. slow_down: poll less often, using the new interval. approved: access_token is a scoped API token for Authorization: Bearer, returned this once. denied or expired: start again.')
op('/api/v1/device','get','getDeviceRequest','Show the app asking to sign in with a code','Account','DeviceRequest',auth=SESSION,params=[parameter('code',string(maxLength=20),required=True)],description='Ten code attempts per minute and thirty per hour per account.')
op('/api/v1/device/decision','post','decideDeviceRequest','Approve or deny an app sign-in with its code','Account',body='DeviceDecision',status=204,auth=SESSION,description='Approval issues the app an API token on its next poll, listed under your tokens. Signing out everywhere before then voids it.')
op('/api/v1/password-recovery','post','recoverPassword','Set a new password with an authenticator or recovery code','Account',body='RecoveryInput',status=204,auth=None,public_origin=True,description='For people who set up an authenticator. Each code works once. Revokes every session, token and pending link. Unknown accounts and wrong codes fail alike (401 recovery_failed). Five attempts per minute per address, and five per hour per account.')
op('/api/v1/me/totp','get','getTOTP','Authenticator recovery status','Account','TOTPStatus',auth=SESSION)
op('/api/v1/me/totp','post','beginTOTP','Start authenticator setup','Account','TOTPSetup','PasswordConfirm',201,SESSION,description='Requires your current password and SECRET_KEY on the server. Confirm with a code to finish.')
op('/api/v1/me/totp/confirm','post','confirmTOTP','Confirm the authenticator and get recovery codes','Account','RecoveryCodes','TOTPConfirm',auth=SESSION,description='Returns ten single-use recovery codes, shown once.')
op('/api/v1/me/totp','delete','disableTOTP','Turn off authenticator recovery','Account',body='PasswordConfirm',status=204,auth=SESSION)
SCHEDULE = enum('manual', 'two_hourly', 'hourly', 'daily', 'weekly')
schemas.update({
    'ServiceKind': obj({'kind': enum(*SERVICE_KINDS), 'name': string(), 'fields': {'type': 'array', 'items': enum('url', 'token', 'username', 'user_id', 'steam_id')},
                        'auth': enum('', 'device', 'pin'), 'available': {'type': 'boolean', 'description': 'False when the server lacks SECRET_KEY or the application key this service needs.'}}),
    'ServiceKindPage': items('ServiceKind'),
    'ServiceLink': obj({'health':enum('unknown','healthy','credentials_rejected','unavailable','rate_limited','not_configured','invalid_response'),'checked_at':NDATE,'id': ID, 'kind': enum(*SERVICE_KINDS), 'name': string(), 'description': string(), 'schedule': SCHEDULE, 'next_run_at': NTIME,
                        'last_run_id': {**string(format='uuid'), 'nullable': True}, 'last_run_at': NTIME, 'last_status': string(), 'push_status': string(), 'created_at': DATE}),
    'ServiceLinkPage': items('ServiceLink'),
    'ServiceLinkInput': obj({'kind': enum(*SERVICE_KINDS), 'name': string(maxLength=80), 'schedule': SCHEDULE,
                             'config': obj({'url': string(maxLength=1000), 'token': string(maxLength=1000), 'username': string(maxLength=1000), 'user_id': string(maxLength=1000), 'steam_id': string(maxLength=1000), 'push': {'type': 'boolean', 'description': 'Jellyfin and Emby: also mark what you watch here as played on the server.'}}, [])}, ['kind', 'schedule', 'config']),
    'ServiceScheduleInput': obj({'schedule': SCHEDULE}, ['schedule']),
    'ServiceAuthorizationInput': obj({'kind': enum('trakt', 'simkl', 'storyteller'), 'url': string(maxLength=500, description='Storyteller only: your server address.')}, ['kind']),
    'ServiceAuthorization': obj({'id': string(format='uuid'), 'kind': enum('trakt', 'simkl', 'storyteller'), 'user_code': string(), 'verification_url': string(), 'interval': integer(minimum=0),
                                 'expires_in': integer(minimum=0), 'status': enum('pending', 'linked', 'expired'), 'link': ref('ServiceLink')},
                                ['id', 'kind', 'user_code', 'verification_url', 'interval', 'expires_in', 'status']),
})
op('/api/v1/me/service-kinds','get','getServiceKinds','Services that can be linked and what each needs','Imports','ServiceKindPage',auth=SESSION)
op('/api/v1/me/services','get','getServices','Your linked services','Imports','ServiceLinkPage',auth=SESSION,description='Credentials are sealed with SECRET_KEY and never returned.')
op('/api/v1/me/services','post','linkService','Link a service with a token, username or server address','Imports','ServiceLink','ServiceLinkInput',201,SESSION,description='One link per service; linking again replaces the saved credentials. A first sync is queued right away. Trakt and Simkl use service-authorizations instead. Media servers on private networks need an administrator or SERVICES_ALLOW_PRIVATE. 503 secret_key_missing or service_unavailable when the server is not set up for it.')
op('/api/v1/me/services/{serviceID}/schedule','put','scheduleService','Change how often a linked service syncs','Imports','ServiceLink','ServiceScheduleInput',auth=SESSION)
op('/api/v1/me/services/{serviceID}','delete','unlinkService','Unlink a service','Imports',status=204,auth=SESSION,description='What it imported stays in your library; roll back its imports to remove that.')
op('/api/v1/me/services/{serviceID}/sync','post','syncService','Sync a linked service now; scope library:write','Imports','ImportRun',status=202,description='Runs an ordinary import: only adds or moves forward, never repeats a play. 409 import_in_progress while another import runs.')
op('/api/v1/me/service-authorizations','post','authorizeService','Start the Trakt or Storyteller device sign-in, or the Simkl PIN sign-in','Imports','ServiceAuthorization','ServiceAuthorizationInput',201,SESSION,description='Show user_code and verification_url, then poll at the given interval until status is linked or expired.')
op('/api/v1/me/service-authorizations/{authorizationID}/poll','post','pollServiceAuthorization','Check whether the sign-in was approved','Imports','ServiceAuthorization',auth=SESSION)
TRACKABLE = enum('movie', 'series', 'anime', 'manga', 'book', 'audiobook', 'comic', 'game', 'boardgame', 'music', 'podcast', 'video')
NDATEONLY = {**string(pattern='^[0-9]{4}-[0-9]{2}-[0-9]{2}$'), 'nullable': True}
PERSON = obj({'username': string(), 'display_name': string()})
schemas.update({
    'DiscoverReason': obj({'code': enum('popular','related','genre','catalog'), 'title': {**string(), 'nullable': True}, 'genre': {**string(), 'nullable': True}, 'source': enum('provider','local')}),
    'DiscoverRow': obj({'key': string(), 'title': string(description='Compatibility fallback; translate reason instead.'), 'reason': ref('DiscoverReason'), 'items': array('CatalogResult')}),
    'DiscoverFeed': obj({'media_type': TRACKABLE, 'rows': array('DiscoverRow'), 'degraded': {'type': 'boolean'}, 'provider': string(), 'availability': enum('available','local_only','not_configured','temporarily_unavailable','search_only')}),
    'DismissInput': obj({'provider': string(maxLength=32), 'media_type': string(maxLength=32), 'provider_id': string(maxLength=200)}),
    'PersonCredit': obj({'provider': string(), 'provider_id': string(), 'media_type': KIND, 'title': string(), 'year': NINT, 'image_url': string(),
        'overview': string(), 'credits': STRINGS, 'catalog_id': NID, 'library_entry_id': NID, 'library_status': {**STATUS, 'nullable': True}, 'role': string()}),
    'PersonPage': obj({'provider': string(), 'id': string(), 'name': string(), 'biography': string(), 'image_url': string(), 'known_for': string(),
                       'birthday': NDATEONLY, 'deathday': NDATEONLY, 'credits': array('PersonCredit')}),
    'StudioPage': obj({'provider': string(), 'kind': enum('company', 'network'), 'id': string(), 'name': string(), 'description': string(), 'logo_url': string(),
                       'country': string(), 'titles': array('CatalogResult')}),
    'RecommendationPerson': PERSON,
    'Recommendation': obj({'id': ID, 'from': ref('RecommendationPerson'), 'to': ref('RecommendationPerson'), 'catalog_id': ID, 'title': string(), 'media_type': KIND,
                           'year': NINT, 'image_url': string(), 'note': string(), 'status': enum('pending', 'accepted', 'dismissed'), 'in_library': {'type': 'boolean'}, 'created_at': DATE}),
    'RecommendationPage': items('Recommendation'),
    'RecommendationInput': obj({'to': string(maxLength=48), 'catalog_id': ID, 'note': string(maxLength=500)}, ['to', 'catalog_id']),
    'RecipientPage': items('RecommendationPerson'),
})
op('/api/v1/discover','get','getDiscover','Suggestions: popular now, related to what you liked, and from your favourite genres; scope library:read','Catalog','DiscoverFeed',params=[parameter('media_type', TRACKABLE, required=True)],
   description='Titles in your library and dismissed suggestions are left out. Retains every nonempty row, capped at 20, with deduplication against displayed titles only. Translate structured reason codes instead of title. Popular/related source=provider; genre/cold-start catalog source=local describes existing shared metadata. availability explains unsupported browsing/configuration before empty-state copy; degraded marks a failed supported popular request.')
schemas.update({'DiscoverDismissal': obj({**schemas['CatalogResult']['properties'], 'created_at': DATE}), 'DiscoverDismissalPage': paged('DiscoverDismissal')})
op('/api/v1/discover/dismissals','get','getDiscoverDismissals','Review your hidden suggestions; scope library:read','Catalog','DiscoverDismissalPage',params=[parameter('media_type',TRACKABLE,required=True), parameter('cursor',string(maxLength=8192)), parameter('limit',integer(minimum=1,maximum=100))],description='Caller-private persistent dismissals ordered by provider/kind/provider_id. Cursor belongs to the caller and media type. Unknown metadata uses provider_id as title; DELETE the same identity to undo. No upstream requests are made.')
op('/api/v1/discover/dismissals','post','dismissSuggestion','Hide a suggestion from Discover; scope library:write','Catalog',body='DismissInput',status=204)
op('/api/v1/discover/dismissals','delete','restoreSuggestion','Show a dismissed suggestion again; scope library:write','Catalog',status=204,params=[parameter('provider', string(maxLength=32), required=True), parameter('media_type', string(maxLength=32), required=True), parameter('provider_id', string(maxLength=200), required=True)])
op('/api/v1/people/{provider}/{personID}','get','getPerson','A cast or crew member with their credits; scope library:read','Catalog','PersonPage',description='Credits are ordered by popularity; catalog_id is set for titles already in the shared catalog. Cached for a day.')
op('/api/v1/studios/{provider}/{studioKind}/{studioID}','get','getStudio','A production company or network with its popular titles; scope library:read','Catalog','StudioPage')
op('/api/v1/recommendations','get','getRecommendations','Recommendations you received or sent','Lists','RecommendationPage',auth=SESSION,params=[parameter('box', enum('inbox', 'all', 'sent'), description='inbox (default) is pending received; all is every received; sent is what you sent.')])
op('/api/v1/recommendations','post','recommendTitle','Recommend a title to another person on this server','Lists','Recommendation','RecommendationInput',201,SESSION,description='Recommending the same title to the same person again updates the note and returns it to their inbox. Thirty per minute.')
op('/api/v1/recommendations/{recommendationID}/accept','post','acceptRecommendation','Accept: the title is added as planned unless you already track it','Lists','Recommendation',auth=SESSION)
op('/api/v1/recommendations/{recommendationID}/dismiss','post','dismissRecommendation','Dismiss a recommendation','Lists','Recommendation',auth=SESSION)
op('/api/v1/recommendation-recipients','get','getRecommendationRecipients','Find people to recommend to by username or name prefix','Lists','RecipientPage',auth=SESSION,params=[parameter('q', string(maxLength=80), required=True, description='At least two characters.')])
for path, names in [('/api/v1/people/{provider}/{personID}', ('provider', 'personID')), ('/api/v1/studios/{provider}/{studioKind}/{studioID}', ('provider', 'studioKind', 'studioID'))]:
    for p_ in paths[path]['get']['parameters']:
        if p_['name'] == 'studioKind': p_['schema'] = enum('company', 'network')
        elif p_['name'] in names: p_['schema'] = string(maxLength=40)
op('/api/v1/catalog/{catalogID}/editions','get','getEditions','Editions of a book or audiobook; scope library:read','Catalog','EditionPage',description='Editions with a cover, ISBN and page count first. Titles whose provider has no editions return an empty list. Cached for a day.')
op('/api/v1/library/{entryID}/edition','put','chooseEdition','Choose the edition you read, or clear it with null; scope library:write','Library','Mutation','EditionChoice',idempotent=True,description='The edition is looked up from the provider. Its page count becomes your total (progress stays within it; a finished title stays finished) and its cover your artwork. Clearing restores the work\'s.')
COLLAB_ROLE = enum('owner', 'editor', 'viewer')
STATUS_ENUM = enum('planned', 'in_progress', 'paused', 'dropped', 'completed', 'rated')
collab_list = {'id': ID, 'name': string(), 'description': string(), 'role': COLLAB_ROLE, 'owner': ref('RecommendationPerson'), 'members': integer(minimum=1),
               'items': integer(minimum=0), 'covers': STRINGS, 'version': integer(minimum=1), 'updated_at': DATE}
schemas.update({
    'CollabList': obj(collab_list),
    'CollabListPage': items('CollabList'),
    'CollabMember': obj({'username': string(), 'display_name': string(), 'role': COLLAB_ROLE, 'added_at': DATE}),
    'CollabItem': obj({'catalog_id': ID, 'title': string(), 'media_type': KIND, 'year': NINT, 'image_url': string(), 'note': string(),
                       'added_by': {'allOf': [ref('RecommendationPerson')], 'nullable': True}, 'added_at': DATE, 'entry_id': NID,
                       'status': {**STATUS_ENUM, 'nullable': True}}),
    'CollabDetail': obj({**collab_list, 'member_list': array('CollabMember'), 'item_list': array('CollabItem')}),
    'CollabInput': obj({'name': string(minLength=1, maxLength=100), 'description': string(maxLength=2000), 'version': integer(minimum=0)}, ['name']),
    'CollabMemberInput': obj({'username': string(maxLength=48), 'role': enum('editor', 'viewer')}),
    'CollabItemInput': obj({'catalog_id': ID, 'note': string(maxLength=500)}, ['catalog_id']),
    'CollabOrder': obj({'catalog_ids': {'type': 'array', 'maxItems': 1000, 'items': ID}}),
})
COLLAB_NOTE = 'Lists you are not a member of answer 404.'
op('/api/v1/collab-lists','get','getCollabLists','Collaborative lists you belong to; scope lists:read','Lists','CollabListPage')
op('/api/v1/collab-lists','post','createCollabList','Start a collaborative list you own; scope lists:write','Lists','CollabList','CollabInput',201,description='Up to 100 owned lists.')
op('/api/v1/collab-lists/{collabID}','get','getCollabList','A collaborative list with its members and titles; scope lists:read','Lists','CollabDetail',description='Each title shows your own tracking of it. '+COLLAB_NOTE)
op('/api/v1/collab-lists/{collabID}','patch','updateCollabList','Rename or describe a collaborative list; owner only','Lists','CollabDetail','CollabInput',description='Send the current version; 409 when someone changed it first.')
op('/api/v1/collab-lists/{collabID}','delete','deleteCollabList','Delete a collaborative list; owner only','Lists',status=204)
op('/api/v1/collab-lists/{collabID}/members','put','setCollabMember','Add someone or change their role; owner only','Lists','CollabDetail','CollabMemberInput',description='Editors add, remove and order titles; viewers only read. Up to 50 people.')
op('/api/v1/collab-lists/{collabID}/members/{username}','delete','removeCollabMember','Remove someone, or leave a list yourself','Lists',status=204,description='The owner removes others; anyone can remove themselves except the owner.')
op('/api/v1/collab-lists/{collabID}/items','post','addCollabItem','Add a catalogued title; owners and editors','Lists','CollabDetail','CollabItemInput',description='Adding a title already on the list updates its note when one is given. Up to 1000 titles.')
op('/api/v1/collab-lists/{collabID}/items/{catalogID}','delete','removeCollabItem','Remove a title; owners and editors','Lists',status=204)
op('/api/v1/collab-lists/{collabID}/order','put','orderCollabList','Order titles; owners and editors','Lists','CollabDetail','CollabOrder',description='Named titles come first in the given order; the rest follow in their current order.')
op('/api/v1/lists/{listID}/source','put','followListSource','Make a manual list follow a public Trakt or MDBList list; scopes lists:write and library:write','Lists','List','ListSourceInput',description='The first sync is queued now and the list is refreshed daily. The source owns the list: its titles are added to your library as planned when new (existing tracking is kept), and the list is set to exactly those titles in the source order.')
op('/api/v1/lists/{listID}/source','delete','unfollowListSource','Stop following; the list keeps its titles; scope lists:write','Lists','List')
op('/api/v1/lists/{listID}/trakt-export','post','exportListToTrakt','Copy a list to a private Trakt list of the same name','Lists','TraktExport',auth=SESSION,description='Needs a linked Trakt account. Movies and shows are added; titles already on the Trakt list stay and nothing is removed there. 409 trakt_not_linked without a linked account.')
op('/api/v1/catalog/{catalogID}/episode-orders','get','getEpisodeOrders','Alternate episode orders of a show (DVD, absolute, story arcs); scope library:read','Catalog','EpisodeOrderPage',description='From TMDB episode groups; cached for a day. Other providers return an empty list.')
op('/api/v1/catalog/{catalogID}/episode-orders/{orderID}','get','getEpisodeOrder','One episode order with its groups and episodes; scope library:read','Catalog','EpisodeOrderDetail',description='Episodes are the show\'s own catalog records, so marking them works as usual. Stored and refreshed weekly.')
op('/api/v1/library/{entryID}/episode-order','put','chooseEpisodeOrder','Follow an alternate episode order, or the aired order with null; scope library:write','Library','Mutation','EpisodeOrderChoice',idempotent=True,description='Plays and progress stay; the order decides which episode is up next.')
for p_ in paths['/api/v1/catalog/{catalogID}/episode-orders/{orderID}']['get']['parameters']:
    if p_['name'] == 'orderID': p_['schema'] = string(maxLength=100)
schemas.update({
    'ChangePlay': obj({'unmapped': {'type': 'boolean'}, 'id': ID, 'entry_id': ID, 'catalog_id': ID, 'played_at': DATE, 'cycle': integer(minimum=1), 'source': string(), 'season': NINT, 'episode': NINT}),
    'Change': obj({'entity': enum('entry', 'play'), 'entity_id': ID, 'op': enum('upsert', 'delete'), 'at': DATE, 'entry': ref('EntrySummary'), 'play': ref('ChangePlay')}, ['entity', 'entity_id', 'op', 'at']),
    'ChangePage': obj({'items': array('Change'), 'next_cursor': string(), 'has_more': {'type': 'boolean'}}),
})
op('/api/v1/changes','get','getChanges','What changed in your library since a cursor; scope library:read','Library','ChangePage',
   params=[parameter('cursor', string(maxLength=40), description='Omit to capture the current cursor BEFORE downloading the library. After downloading, replay all changes from that cursor, applying upserts and deletes by ID.'), parameter('limit', integer(minimum=1, maximum=500, default=200))],
   description='For sync clients. Entries and plays that changed, oldest first, including deletions; upserts carry the current record. Call again with next_cursor while has_more. Changes are kept for 90 days; an older cursor answers 410 cursor_expired, and the client downloads the library again.')
paths['/api/v1/changes']['get']['responses']['410'] = response('Problem', 'The cursor is older than the feed keeps')
paths.setdefault('/mcp', {})['post'] = {'operationId': 'mcp', 'summary': 'Model Context Protocol endpoint for assistants', 'tags': ['Floppy compatibility'], 'security': COMPAT,
    'description': 'Streamable HTTP transport with JSON responses (protocol 2025-06-18). Authenticate with a personal token; tools follow its scopes: search_catalog, list_library, up_next, history and stats need library:read, add_to_library and record_play need library:write. Notifications are answered 202.',
    'requestBody': {'required': True, 'content': {'application/json': {'schema': LOOSE}}},
    'responses': {'200': {'description': 'JSON-RPC response', 'content': {'application/json': {'schema': LOOSE}}}, '202': {'description': 'Notification accepted'},
                  '400': {'description': 'Not a JSON-RPC request', 'content': {'application/json': {'schema': LOOSE}}}, '401': response('Problem', 'Missing or invalid token'), '429': response('Problem', 'Rate limited')}}
EXPORT_FORMAT = enum('jsonl', 'csv', 'letterboxd')
schemas.update({
    'ExportScheduleValue': obj({'frequency': enum('daily', 'weekly', 'monthly'), 'format': EXPORT_FORMAT, 'keep': integer(minimum=1, maximum=20), 'next_run_at': DATE, 'version': integer(minimum=0)}, ['frequency', 'format', 'keep']),
    'ExportSchedule': obj({'schedule': {'allOf': [ref('ExportScheduleValue')], 'nullable': True}}),
    'ExportFile': obj({'id': string(format='uuid'), 'format': EXPORT_FORMAT, 'trigger': enum('schedule', 'manual'), 'entries': integer(minimum=0), 'size_bytes': integer(minimum=0), 'created_at': DATE}),
    'ExportFilePage': items('ExportFile'),
    'ExportFormatInput': obj({'format': EXPORT_FORMAT}),
})
op('/api/v1/me/export-schedule','get','getExportSchedule','Your automatic export schedule, if any','Imports','ExportSchedule',auth=SESSION)
op('/api/v1/me/export-schedule','put','saveExportSchedule','Keep automatic exports of your library','Imports','ExportSchedule','ExportScheduleValue',auth=SESSION,description='The first snapshot is written within fifteen minutes; later ones follow the frequency. Older snapshots beyond keep are removed.')
op('/api/v1/me/export-schedule','delete','deleteExportSchedule','Stop automatic exports','Imports',status=204,auth=SESSION,description='Snapshots already kept stay until you delete them.')
op('/api/v1/export-files','get','getExportFiles','Library snapshots kept on the server; scope library:read','Imports','ExportFilePage')
op('/api/v1/export-files','post','createExportFile','Keep a snapshot of your library now','Imports','ExportFile','ExportFormatInput',201,SESSION,description='Compressed on the server; at most 64 MiB after compression. Ten per minute.')
op('/api/v1/export-files/{fileID}','get','downloadExportFile','Download a kept snapshot; scope library:read','Imports',description='Decompressed on the way out.')
paths['/api/v1/export-files/{fileID}']['get']['responses']['200'] = {'description': 'Download', 'content': {'application/x-ndjson': {'schema': string()}, 'text/csv': {'schema': string()}}}
op('/api/v1/export-files/{fileID}','delete','deleteExportFile','Delete a kept snapshot','Imports',status=204,auth=SESSION)
op('/api/v1/lists','get','getLists','Your private lists; scope lists:read','Lists','ListPage',params=PAGINATION)
op('/api/v1/lists','post','createList','Create a private list; scope lists:write','Lists','Mutation','ListInput',201,idempotent=True)
op('/api/v1/library/{entryID}/lists','get','getEntryLists','Your lists that hold one of your titles; scopes library:read and lists:read','Lists','ListItems',description='Manual lists the title is a member of and smart lists whose rules select it, by name. Not paginated: a profile has at most 1,000 lists.')
op('/api/v1/lists/{listID}','get','getList','Read your list metadata; scope lists:read','Lists','List')
op('/api/v1/lists/{listID}','put','replaceList','Replace your list metadata; scope lists:write','Lists','Mutation','ListInput',idempotent=True,description='Requires current version. Omitted description resets it to empty.')
op('/api/v1/lists/{listID}','delete','deleteList','Delete a list, preserving library items; scope lists:write','Lists','Mutation','VersionInput',idempotent=True)
op('/api/v1/lists/{listID}/entries/{entryID}','put','setListMembership','Set membership of your item in your list','Lists','Mutation','MembershipInput',idempotent=True,description='Requires lists:write and library:read. Composite foreign keys enforce same-account membership. Requires current list version. A no-op does not change it; removing a library item automatically removes its memberships.')

GUIDE = """Pastime is a private, multi-account media tracker. This reference documents every operation the server implements.

## Authentication
- **Browser session.** Sign in with `POST /api/v1/login`. Session mutations must send the exact `Origin` and the `X-CSRF-Token` from `GET /api/v1/session`. This page adds both automatically.
- **Profile login.** The household's primary user can give a profile its own login name and password. Signing in with it opens a browser session that acts only for that profile: it can't switch or list other profiles, and household settings, collaborative lists, recommendations, single sign-on links, authenticator recovery and administration answer 403.
- **Personal token.** Create one under Account → Tokens and send `Authorization: Bearer <token>`. Tokens carry scopes (`library:read`, `library:write`, `lists:read`, `lists:write`, `profile:read`) and expire. They never administer accounts. Never combine a token with a session cookie.

Every request is checked against current PostgreSQL state, so revoked or disabled credentials stop working immediately.

## Conventions
- **IDs** are decimal strings (or UUIDs where noted), never JSON numbers.
- **Lists** are bounded. Paged lists return `next_cursor`; pass it back as `cursor` with the same filters. Cursors are opaque and are not sync checkpoints.
- **Writes that create or change library state** take an `Idempotency-Key` (a UUID per intent). Retrying with the same key replays the first outcome; a different intent with the same key returns 409.
- **Version fencing.** Updates carry the record's current `version`; a stale version returns 409 so no change is silently overwritten.
- **Errors** use one shape, `{"code": "...", "message": "..."}`. The `code` is stable; the message is for people.
- **Rate limits** return 429 with `Retry-After`.

## Privacy
Administrators manage accounts, jobs and the shared catalog. They cannot read anyone's library, notes, lists or history through this API."""
TAGS = [
    ('Runtime', 'Liveness, readiness and server identity. No authentication.'),
    ('Account', 'Your session, profile, password, sessions, personal tokens and calendar feed.'),
    ('Invitations', 'Preview and claim one-use join and password-reset links.'),
    ('Library', 'Your tracked titles: create, update progress, record plays, restart and delete.'),
    ('Catalog', 'Shared public metadata, provider capabilities and caller-private discovery annotations.'),
    ('Insights', 'Home, history, statistics and the release calendar.'),
    ('Lists', 'Manual and smart lists, their order, notes and sharing.'),
    ('Notifications', 'Release notices by webhook, ntfy, Discord, Gotify or Apprise.'),
    ('Collection', 'Owned copies with format, edition and purchase details, and your custom fields.'),
    ('Public lists', 'Read-only access to lists their owners chose to share. No authentication.'),
    ('Imports', 'Imports from other services and exports of your data.'),
    ('Connections', 'Plex, Jellyfin, Emby, Kodi and request-manager webhooks that record what you watch and want.'),
    ('Administration', 'Accounts, invitations, audit, runtime health, background jobs, providers, catalog maintenance and import runs. Admin or owner session only.'),
    ('Floppy compatibility', 'Endpoints for the Silo and Bloem watch-provider plugin, ListenBrainz scrobblers and MCP assistants. Not part of the native API.'),
    ('KOReader', 'KOReader progress sync server. Devices authenticate with x-auth-user and x-auth-key headers.'),
]
spec = {
    'openapi': '3.0.3',
    'info': {'title': 'Pastime API', 'version': '0.3.0-dev', 'description': GUIDE,
             'license': {'name': 'AGPL-3.0-only', 'url': 'https://www.gnu.org/licenses/agpl-3.0.html'}},
    'tags': [{'name': n, 'description': d} for n, d in TAGS],
    'x-tagGroups': [{'name': 'Personal', 'tags': ['Account', 'Library', 'Collection', 'Catalog', 'Insights', 'Lists', 'Notifications', 'Imports', 'Connections']},
                    {'name': 'Access', 'tags': ['Invitations', 'Administration']},
                    {'name': 'Platform', 'tags': ['Public lists', 'Runtime', 'Floppy compatibility', 'KOReader']}],
    'servers': [{'url': '/'}], 'paths': paths,
    'components': {'schemas': schemas, 'securitySchemes': {
        'SessionCookie': {'type':'apiKey','in':'cookie','name':'pastime_session','description':'HttpOnly development cookie; HTTP is accepted only on loopback origins. Sign in through the application.'},
        'SecureSessionCookie': {'type':'apiKey','in':'cookie','name':'__Host-pastime_session','description':'HTTPS HttpOnly cookie. No Domain attribute. Browser mutations also require CSRF and exact Origin.'},
        'BearerToken': {'type':'http','scheme':'bearer','description':'Scoped expiring personal token. Never grants account/administration authority. Omit ambient session cookies.'},
        'BloemSyncCredential': {'type':'http','scheme':'bearer','description':'Backend-only source/profile-bound tracking credential. Cannot authorize browser or administrative access.'},
        'BloemProvisionCredential': {'type':'http','scheme':'bearer','description':'Backend-only registered instance enrollment credential. Never send to browser clients. Does not authorize ordinary account/library APIs.'},
        'CompatToken': {'type':'apiKey','in':'header','name':'Authorization','description':'Compatibility endpoints also accept "Token <personal token>".'},
    }},
}
def yaml(value, indent=0):
    pad = ' ' * indent
    if isinstance(value, dict):
        if not value: return pad + '{}\n'
        result = ''
        for key, item in value.items():
            label = json.dumps(str(key))
            if isinstance(item, (dict,list)) and item:
                result += pad + label + ':\n' + yaml(item, indent+2)
            else: result += pad + label + ': ' + json.dumps(item, ensure_ascii=False) + '\n'
        return result
    if isinstance(value, list):
        return ''.join(pad + '-\n' + yaml(item, indent+2) if isinstance(item,(dict,list)) else pad+'- '+json.dumps(item,ensure_ascii=False)+'\n' for item in value)
    return pad + json.dumps(value,ensure_ascii=False) + '\n'

if __name__ == '__main__':
    parser=argparse.ArgumentParser(); parser.add_argument('--output',type=Path,default=ROOT/'internal/httpapi/openapi.yaml'); args=parser.parse_args()
    args.output.write_text('# Generated by tools/api-contract.py; edit that source and run make generate.\n'+yaml(spec))
    print(f'Wrote {len(paths)} paths / {sum(len(v) for v in paths.values())} operations to {args.output}')
