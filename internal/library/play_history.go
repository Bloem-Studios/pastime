package library

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/Bloem-Studios/pastime/internal/identity"
)

// TitleHistoryItem is one play or finished session of a title, newest first in TitleHistory.
type TitleHistoryItem struct {
	Kind string `json:"kind"` // play or session
	// PlayID identifies a play for deletion; a session has none.
	PlayID *int64 `json:"play_id,string"`
	// CatalogID is the record played or read: the title itself, or its episode or issue.
	CatalogID     int64          `json:"catalog_id,string"`
	SeasonNumber  *int           `json:"season_number"`
	EpisodeNumber *int           `json:"episode_number"`
	At            time.Time      `json:"at"`
	Minutes       *int           `json:"minutes,omitempty"`
	Amount        *int           `json:"amount,omitempty"`
	Source        string         `json:"source"`
	ItemTitle     string         `json:"item_title,omitempty"`
	Details       map[string]any `json:"details"`
	// Marked is a bulk "mark as played": listed, but not in the totals.
	Marked bool `json:"marked"`
}

// PlayerRecord counts how often a board game player played and won.
type PlayerRecord struct {
	Name  string `json:"name"`
	Plays int    `json:"plays"`
	Wins  int    `json:"wins"`
}

// TitleHistory is a title's plays and sessions with their totals, across every cycle.
type TitleHistory struct {
	Items    []TitleHistoryItem `json:"items"`
	Plays    int                `json:"plays"`
	Sessions int                `json:"sessions"`
	Minutes  int                `json:"minutes"`
	Players  []PlayerRecord     `json:"players"`
}

const historyLimit = 500

// PlayHistory lists the caller's plays and finished sessions of one library entry.
// Totals count every play and session; the list holds the newest 500. A session that
// recorded a play (the one that finished the work) appears once, as that play.
func (s Store) PlayHistory(ctx context.Context, a identity.Actor, entryID int64) (TitleHistory, error) {
	out := TitleHistory{Items: []TitleHistoryItem{}, Players: []PlayerRecord{}}
	if !a.Allows("library:read") {
		return out, fault.Forbidden
	}
	var exists bool
	if err := s.Pool.QueryRow(ctx, "SELECT true FROM library_entries WHERE profile_id=$1 AND id=$2", a.TrackingID(), entryID).Scan(&exists); errors.Is(err, pgx.ErrNoRows) {
		return out, fault.NotFound
	} else if err != nil {
		return out, err
	}
	rows, err := s.Pool.Query(ctx, `SELECT 'play',p.id,c.id,c.season_number,c.episode_number,p.played_at,p.minutes,NULL::integer,p.source,CASE WHEN c.parent_id IS NOT NULL THEN c.title ELSE '' END,COALESCE(p.details,'{}'::jsonb),p.marked
		FROM plays p JOIN catalog_items c ON c.id=p.catalog_item_id WHERE p.profile_id=$1 AND p.entry_id=$2
		UNION ALL SELECT 'session',NULL::bigint,c.id,c.season_number,c.episode_number,k.finished_at,k.minutes,k.amount,'checkin',CASE WHEN c.parent_id IS NOT NULL THEN c.title ELSE '' END,k.details,false
		FROM checkins k JOIN catalog_items c ON c.id=k.catalog_item_id WHERE k.profile_id=$1 AND k.entry_id=$2 AND k.kind='session' AND k.status='finished' AND k.play_id IS NULL
		ORDER BY 6 DESC,2 DESC NULLS LAST`, a.TrackingID(), entryID)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	players := map[string]*PlayerRecord{}
	for rows.Next() {
		var it TitleHistoryItem
		if err = rows.Scan(&it.Kind, &it.PlayID, &it.CatalogID, &it.SeasonNumber, &it.EpisodeNumber, &it.At, &it.Minutes, &it.Amount, &it.Source, &it.ItemTitle, &it.Details, &it.Marked); err != nil {
			return out, err
		}
		if it.Details == nil {
			it.Details = map[string]any{}
		}
		if it.Marked {
			if len(out.Items) < historyLimit {
				out.Items = append(out.Items, it)
			}
			continue
		}
		if it.Kind == "play" {
			out.Plays++
		} else {
			out.Sessions++
		}
		if it.Minutes != nil {
			out.Minutes += *it.Minutes
		}
		seats, _ := it.Details["players"].([]any)
		for _, seat := range seats {
			m, _ := seat.(map[string]any)
			name, _ := m["name"].(string)
			if name = strings.TrimSpace(name); name == "" {
				continue
			}
			key := strings.ToLower(name)
			p, ok := players[key]
			if !ok {
				p = &PlayerRecord{Name: name}
				players[key] = p
			}
			p.Plays++
			if won, _ := m["winner"].(bool); won {
				p.Wins++
			}
		}
		if len(out.Items) < historyLimit {
			out.Items = append(out.Items, it)
		}
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	for _, p := range players {
		out.Players = append(out.Players, *p)
	}
	sort.Slice(out.Players, func(i, j int) bool {
		a, b := out.Players[i], out.Players[j]
		if a.Wins != b.Wins {
			return a.Wins > b.Wins
		}
		if a.Plays != b.Plays {
			return a.Plays > b.Plays
		}
		return strings.ToLower(a.Name) < strings.ToLower(b.Name)
	})
	return out, nil
}
