package bloem

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"slices"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Bloem-Studios/pastime/internal/fault"
)

type PersonalRead struct {
	Kinds     []string `json:"kinds"`
	Cursor    string   `json:"cursor,omitempty"`
	PageToken string   `json:"page_token,omitempty"`
	PageSize  int      `json:"page_size"`
}

// PersonalRemoteItem is one personal state of a title. Watched and progress
// items are movies or episodes; an episode names its show through
// SeriesExternalIDs and its place by season and episode number. ChangedAt is
// the last play of a watched item and the time of a resume point.
type PersonalRemoteItem struct {
	ProviderItemKey   string            `json:"provider_item_key"`
	Kind              string            `json:"kind"`
	MediaKind         string            `json:"media_kind"`
	ExternalIDs       map[string]string `json:"external_ids"`
	SeriesExternalIDs map[string]string `json:"series_external_ids,omitempty"`
	SeasonNumber      *int32            `json:"season_number,omitempty"`
	EpisodeNumber     *int32            `json:"episode_number,omitempty"`
	Removed           bool              `json:"removed"`
	Rating            *int              `json:"rating,omitempty"`
	Position          *int32            `json:"position,omitempty"`
	PlayCount         *int              `json:"play_count,omitempty"`
	ProgressPercent   *float64          `json:"progress_percent,omitempty"`
	ChangedAt         time.Time         `json:"changed_at"`
}

// personalFamilies are the state families state:read serves. An empty request
// keeps meaning the three list and rating families, as plugin 0.2.0 expects.
var personalFamilies = []string{"favorite", "watchlist", "rating", "watched", "progress"}

// bloemSource marks plays and resume points Pastime received from Bloem. They
// are never offered back to Bloem.
const bloemSource = "bloem"

// Watched plays and resume points of movies and of episodes of a series, from
// every source except Bloem. Unmapped plays have no trustworthy title.
const personalWatchedSQL = `SELECT c.id,c.kind,c.provider,c.provider_id,c.external_ids,r.provider,r.provider_id,r.external_ids,c.season_number,c.episode_number,count(*),max(p.played_at)
 FROM plays p JOIN catalog_items c ON c.id=p.catalog_item_id JOIN catalog_items r ON r.id=c.root_id
 WHERE p.profile_id=$1 AND p.source<>$2 AND NOT p.unmapped AND (c.kind='movie' OR (c.kind='episode' AND r.kind='series'))
 GROUP BY c.id,r.id ORDER BY max(p.played_at),c.id LIMIT 10001`

const personalProgressSQL = `SELECT c.id,c.kind,c.provider,c.provider_id,c.external_ids,r.provider,r.provider_id,r.external_ids,c.season_number,c.episode_number,pp.position_seconds,COALESCE(pp.duration_seconds,c.runtime_minutes*60,r.runtime_minutes*60),pp.updated_at
 FROM playback_progress pp JOIN catalog_items c ON c.id=pp.catalog_item_id JOIN catalog_items r ON r.id=c.root_id
 WHERE pp.profile_id=$1 AND pp.source<>$2 AND (c.kind='movie' OR (c.kind='episode' AND r.kind='series'))
 ORDER BY pp.updated_at,c.id LIMIT 10001`

// Movies and shows the profile removed from its library (watch_removals), served
// as watched removals until the profile plays the title again.
const personalWatchRemovalsSQL = `SELECT c.id,c.kind,c.provider,c.provider_id,c.external_ids,w.removed_at
 FROM watch_removals w JOIN catalog_items c ON c.id=w.catalog_item_id
 WHERE w.profile_id=$1 AND c.kind IN ('movie','series')
 AND NOT EXISTS(SELECT 1 FROM plays p JOIN catalog_items pc ON pc.id=p.catalog_item_id
  WHERE p.profile_id=w.profile_id AND (pc.id=c.id OR pc.root_id=c.id) AND p.played_at>w.removed_at)
 ORDER BY w.removed_at,c.id LIMIT 10001`

const missingVideoIDs = "Some Pastime titles have no stable video identifier; missing memberships will be preserved."

// stableVideoIDs returns a title's TMDB, IMDb and TVDB identifiers.
func stableVideoIDs(provider, providerID string, ids map[string]any) map[string]string {
	out := map[string]string{}
	for _, ns := range []string{"tmdb", "imdb", "tvdb"} {
		if v, ok := ids[ns].(string); ok && v != "" {
			out[ns] = v
		}
		if ns == provider {
			out[ns] = providerID
		}
	}
	return out
}

// videoRow scans one watched or progress row's identity and reports whether
// Bloem can match it: a movie needs its own identifiers, an episode its show's
// identifiers plus season and episode numbers.
type videoRow struct {
	id                           int64
	kind, provider, providerID   string
	rootProvider, rootProviderID string
	ids, rootIDs                 map[string]any
	season, episode              *int32
}

func (v videoRow) item(kind string) (PersonalRemoteItem, bool) {
	it := PersonalRemoteItem{ProviderItemKey: "catalog:" + strconv.FormatInt(v.id, 10), Kind: kind, MediaKind: v.kind, ExternalIDs: stableVideoIDs(v.provider, v.providerID, v.ids)}
	if v.kind == "movie" {
		return it, len(it.ExternalIDs) > 0
	}
	it.SeriesExternalIDs = stableVideoIDs(v.rootProvider, v.rootProviderID, v.rootIDs)
	it.SeasonNumber, it.EpisodeNumber = v.season, v.episode
	return it, len(it.SeriesExternalIDs) > 0 && v.season != nil && v.episode != nil
}

type PersonalPage struct {
	Items            []PersonalRemoteItem `json:"items"`
	NextPageToken    string               `json:"next_page_token"`
	NextCursor       string               `json:"next_cursor"`
	CompleteSnapshot bool                 `json:"complete_snapshot"`
	Warnings         []string             `json:"warnings"`
}

func (s Store) ReadPersonalState(ctx context.Context, raw string, r PersonalRead) (PersonalPage, error) {
	out := PersonalPage{Items: []PersonalRemoteItem{}, Warnings: []string{}}
	if len(r.Kinds) == 0 {
		r.Kinds = []string{"favorite", "watchlist", "rating"}
	}
	if len(r.Kinds) > len(personalFamilies) || r.Cursor != "" && r.Cursor != "full-v1" || r.PageSize < 0 || r.PageSize > 100 || len(r.PageToken) > 128 {
		return out, fault.Invalid("Invalid personal state read.")
	}
	slices.Sort(r.Kinds)
	r.Kinds = slices.Compact(r.Kinds)
	for _, kind := range r.Kinds {
		if !slices.Contains(personalFamilies, kind) {
			return out, fault.Invalid("Unsupported personal state family.")
		}
	}
	if r.PageSize == 0 {
		r.PageSize = 100
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return out, err
	}
	defer tx.Rollback(ctx)
	a, err := authenticateSyncTx(ctx, tx, raw)
	if err != nil {
		return out, err
	}
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('pastime.change.'||$1::bigint::text,0))`, a.ProfileID); err != nil {
		return out, err
	}
	// This cleanup is confined to this profile. PostgreSQL owns traversal state.
	if _, err = tx.Exec(ctx, `DELETE FROM bloem_personal_read_snapshots WHERE instance_id=$1 AND profile_id=$2 AND expires_at<=clock_timestamp()`, a.InstanceID, a.ProfileID); err != nil {
		return out, err
	}
	var snapshot string
	var payload []byte
	ordinal := 0
	if r.PageToken != "" {
		err = tx.QueryRow(ctx, `SELECT s.id::text,s.items,s.complete_snapshot,s.warnings,p.ordinal FROM bloem_personal_read_pages p JOIN bloem_personal_read_snapshots s ON s.id=p.snapshot_id WHERE p.token::text=$1 AND s.instance_id=$2 AND s.profile_id=$3 AND s.credential_id=$4 AND s.kinds=$5 AND s.page_size=$6 AND s.expires_at>clock_timestamp()`, r.PageToken, a.InstanceID, a.ProfileID, a.CredentialID, r.Kinds, r.PageSize).Scan(&snapshot, &payload, &out.CompleteSnapshot, &out.Warnings, &ordinal)
		if errors.Is(err, pgx.ErrNoRows) {
			return out, fault.Invalid("This personal state traversal is no longer available.")
		}
		if err != nil {
			return out, err
		}
	} else {
		items := []PersonalRemoteItem{}
		out.CompleteSnapshot = true
		if slices.Contains(r.Kinds, "rating") {
			var limited bool
			if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM library_entries e JOIN catalog_items c ON c.id=e.catalog_item_id WHERE e.profile_id=$1 AND c.kind IN ('movie','series') AND e.rating IS NOT NULL AND (e.rating=0 OR e.rating%2<>0))`, a.ProfileID).Scan(&limited); err != nil {
				return out, err
			}
			if limited {
				return out, &fault.Error{Status: 409, Code: "rating_precision", Message: "A Pastime rating has no exact Silo equivalent. Ratings synchronization is paused until it is changed explicitly in Pastime."}
			}
		}
		skipped := func() {
			out.CompleteSnapshot = false
			out.Warnings = []string{missingVideoIDs}
		}
		if slices.ContainsFunc(r.Kinds, func(k string) bool { return k == "favorite" || k == "watchlist" || k == "rating" }) {
			rows, err := tx.Query(ctx, `SELECT c.id,c.kind,c.provider,c.provider_id,c.external_ids,m.kind,NOT m.present,NULL::integer,m.position,m.changed_at FROM profile_catalog_memberships m JOIN catalog_items c ON c.id=m.catalog_item_id WHERE m.profile_id=$1 AND m.kind=ANY($2) AND c.kind IN ('movie','series')
 UNION ALL SELECT c.id,c.kind,c.provider,c.provider_id,c.external_ids,'rating',e.rating IS NULL,e.rating,NULL::integer,COALESCE(e.scored_at,e.updated_at) FROM library_entries e JOIN catalog_items c ON c.id=e.catalog_item_id WHERE e.profile_id=$1 AND 'rating'=ANY($2) AND (e.rating IS NOT NULL OR e.scored_at IS NOT NULL) AND c.kind IN ('movie','series')
 ORDER BY 6,9 NULLS LAST,10,1 LIMIT 10001`, a.ProfileID, r.Kinds)
			if err != nil {
				return out, err
			}
			for rows.Next() {
				var id int64
				var provider, providerID string
				var ids map[string]any
				var item PersonalRemoteItem
				if err = rows.Scan(&id, &item.MediaKind, &provider, &providerID, &ids, &item.Kind, &item.Removed, &item.Rating, &item.Position, &item.ChangedAt); err != nil {
					rows.Close()
					return out, err
				}
				item.ProviderItemKey = "catalog:" + strconv.FormatInt(id, 10)
				item.ExternalIDs = stableVideoIDs(provider, providerID, ids)
				if len(item.ExternalIDs) == 0 {
					skipped()
					continue
				}
				items = append(items, item)
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return out, err
			}
		}
		if slices.Contains(r.Kinds, "watched") {
			rows, err := tx.Query(ctx, personalWatchedSQL, a.ProfileID, bloemSource)
			if err != nil {
				return out, err
			}
			for rows.Next() {
				var v videoRow
				var plays int
				var last time.Time
				if err = rows.Scan(&v.id, &v.kind, &v.provider, &v.providerID, &v.ids, &v.rootProvider, &v.rootProviderID, &v.rootIDs, &v.season, &v.episode, &plays, &last); err != nil {
					rows.Close()
					return out, err
				}
				item, ok := v.item("watched")
				if !ok {
					skipped()
					continue
				}
				item.PlayCount, item.ChangedAt = &plays, last
				items = append(items, item)
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return out, err
			}
			rows, err = tx.Query(ctx, personalWatchRemovalsSQL, a.ProfileID)
			if err != nil {
				return out, err
			}
			for rows.Next() {
				var id int64
				var provider, providerID string
				var ids map[string]any
				item := PersonalRemoteItem{Kind: "watched", Removed: true}
				if err = rows.Scan(&id, &item.MediaKind, &provider, &providerID, &ids, &item.ChangedAt); err != nil {
					rows.Close()
					return out, err
				}
				item.ProviderItemKey = "removed:catalog:" + strconv.FormatInt(id, 10)
				item.ExternalIDs = stableVideoIDs(provider, providerID, ids)
				if len(item.ExternalIDs) == 0 {
					skipped()
					continue
				}
				items = append(items, item)
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return out, err
			}
		}
		if slices.Contains(r.Kinds, "progress") {
			rows, err := tx.Query(ctx, personalProgressSQL, a.ProfileID, bloemSource)
			if err != nil {
				return out, err
			}
			for rows.Next() {
				var v videoRow
				var position int
				var duration *int
				var at time.Time
				if err = rows.Scan(&v.id, &v.kind, &v.provider, &v.providerID, &v.ids, &v.rootProvider, &v.rootProviderID, &v.rootIDs, &v.season, &v.episode, &position, &duration, &at); err != nil {
					rows.Close()
					return out, err
				}
				// A resume point without a known length cannot become a
				// percentage, and one at or past the end is a finished play.
				if duration == nil || *duration <= 0 || position <= 0 {
					continue
				}
				percent := math.Round(float64(position)/float64(*duration)*10000) / 100
				if percent >= 100 {
					continue
				}
				item, ok := v.item("progress")
				if !ok {
					skipped()
					continue
				}
				item.ProgressPercent, item.ChangedAt = &percent, at
				items = append(items, item)
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return out, err
			}
		}
		if len(items) > 10000 {
			return out, fault.Invalid("This profile exceeds the personal state snapshot limit.")
		}
		payload, err = json.Marshal(items)
		if err != nil {
			return out, err
		}
		if len(payload) > 8<<20 {
			return out, fault.Invalid("This personal state snapshot exceeds the payload limit.")
		}
		// A complete single page has no continuation token to replay, so it
		// needs no retained snapshot. Preserve the quota for actual traversals.
		if len(items) <= r.PageSize {
			out.Items = items
			out.NextCursor = "full-v1"
			return out, tx.Commit(ctx)
		}
		var active int
		if err = tx.QueryRow(ctx, `SELECT count(*) FROM bloem_personal_read_snapshots WHERE instance_id=$1 AND profile_id=$2 AND expires_at>clock_timestamp()`, a.InstanceID, a.ProfileID).Scan(&active); err != nil {
			return out, err
		}
		if active >= 16 {
			return out, fault.Limited
		}
		err = tx.QueryRow(ctx, `INSERT INTO bloem_personal_read_snapshots(instance_id,profile_id,credential_id,kinds,page_size,complete_snapshot,warnings,items) VALUES($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id::text`, a.InstanceID, a.ProfileID, a.CredentialID, r.Kinds, r.PageSize, out.CompleteSnapshot, out.Warnings, payload).Scan(&snapshot)
		if err != nil {
			return out, err
		}
	}
	var items []PersonalRemoteItem
	if err = json.Unmarshal(payload, &items); err != nil {
		return out, err
	}
	if ordinal > len(items) {
		return out, fault.Invalid("Invalid personal state continuation.")
	}
	end := min(len(items), ordinal+r.PageSize)
	out.Items = items[ordinal:end]
	if end < len(items) {
		err = tx.QueryRow(ctx, `INSERT INTO bloem_personal_read_pages(snapshot_id,ordinal) VALUES($1,$2) ON CONFLICT(snapshot_id,ordinal) DO UPDATE SET ordinal=EXCLUDED.ordinal RETURNING token::text`, snapshot, end).Scan(&out.NextPageToken)
		if err != nil {
			return out, err
		}
	} else {
		out.NextCursor = "full-v1"
	}
	return out, tx.Commit(ctx)
}
