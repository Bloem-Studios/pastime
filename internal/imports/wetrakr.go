package imports

import (
	"crypto/sha256"
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/Bloem-Studios/pastime/internal/catalog"
)

// ParseWeTrakr accepts individual CSV exports or a ZIP of CSV files. Explicit
// source IDs, dates and list names are preserved; absent files never remove data.
func ParseWeTrakr(data []byte) ([]Record, error) {
	files := map[string][]byte{"export.csv": data}
	var err error
	if len(data) > 2 && string(data[:2]) == "PK" {
		files, err = readZip(data, true)
		if err != nil {
			return nil, err
		}
	}
	names := []string{}
	for name := range files {
		if strings.EqualFold(path.Ext(name), ".csv") {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	if len(names) == 0 {
		return nil, ErrFormat
	}
	var merged merger
	total := 0
	for _, name := range names {
		rows, err := csvRows(files[name])
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			total++
			if total > 100000 {
				return nil, fmt.Errorf("%w: export exceeds 100,000 rows", ErrFormat)
			}
			normalized := map[string]string{}
			for k, v := range row {
				normalized[strings.NewReplacer(" ", "_", "-", "_").Replace(strings.ToLower(k))] = v
			}
			get := func(keys ...string) string {
				for _, k := range keys {
					if v := normalized[k]; v != "" {
						return v
					}
				}
				return ""
			}
			kind := strings.ToLower(get("media_type", "type", "kind"))
			if kind == "tv" || kind == "show" || kind == "episode" || kind == "season" {
				kind = "series"
			}
			if kind == "" {
				if strings.Contains(strings.ToLower(name), "movie") {
					kind = "movie"
				} else if strings.Contains(strings.ToLower(name), "show") || strings.Contains(strings.ToLower(name), "episode") {
					kind = "series"
				}
			}
			if kind != "movie" && kind != "series" {
				return nil, fmt.Errorf("%w: WeTrakr rows need movie or show type", ErrFormat)
			}
			title := get("show_title", "series_title", "title", "name")
			tmdb := get("show_tmdb_id", "series_tmdb_id", "tmdb_id", "tmdb")
			imdb := get("show_imdb_id", "imdb_id", "imdb")
			if title == "" && tmdb == "" && imdb == "" {
				return nil, ErrFormat
			}
			if tmdb != "" {
				if n, e := strconv.ParseInt(tmdb, 10, 64); e != nil || n < 1 {
					return nil, ErrFormat
				}
			}
			identity := tmdb
			if identity == "" {
				identity = imdb
			}
			if identity == "" {
				identity = strings.ToLower(title) + ":" + get("year")
			}
			key := kind + ":" + identity
			rec := merged.get(key, func() Record {
				return Record{Kind: kind, Label: title, Video: &catalog.Reference{Kind: kind, TMDB: tmdb, IMDB: imdb, Title: title, Year: year(get("year")), ShowTMDB: tmdb, ShowIMDB: imdb, ShowTitle: title}}
			})
			status := strings.ToLower(get("status", "watch_status"))
			switch status {
			case "watched", "completed":
				rec.Status = "completed"
			case "watching", "in progress", "in_progress":
				rec.Status = "in_progress"
			case "paused", "on hold", "on_hold":
				rec.Status = "paused"
			case "dropped":
				rec.Status = "dropped"
			case "planning", "planned", "watchlist", "plan to watch":
				if rec.Status == "" {
					rec.Status = "planned"
				}
			}
			if v := get("rating", "score"); v != "" {
				n := score(v)
				if n == nil && v != "0" {
					return nil, ErrFormat
				}
				if v == "0" {
					n = intp(0)
				}
				at, ok := parseDate(get("rated_at", "scored_at", "rating_date"))
				if rec.Rating == nil || ok && (rec.ScoredAt == nil || at.After(*rec.ScoredAt)) {
					rec.Rating = n
					if ok {
						rec.ScoredAt = &at
					}
				}
			}
			if note := get("notes", "review", "comment"); note != "" && !strings.Contains(rec.Notes, note) {
				if rec.Notes != "" {
					rec.Notes += "\n\n"
				}
				rec.Notes += note
			}
			list := get("list_name", "list")
			if list == "" && strings.Contains(strings.ToLower(name), "lists/") {
				list = strings.TrimSuffix(path.Base(name), path.Ext(name))
			}
			if list != "" {
				found := false
				for _, v := range rec.Lists {
					found = found || v == list
				}
				if !found {
					rec.Lists = append(rec.Lists, list)
				}
			}
			date := get("watched_at", "watched_date", "finished_at", "completed_at")
			if date != "" {
				at, ok := parseDate(date)
				if !ok {
					return nil, fmt.Errorf("%w: invalid watch date", ErrFormat)
				}
				play := PlayRef{At: at}
				season, episode := get("season", "season_number"), get("episode", "episode_number")
				if season != "" || episode != "" {
					sn, e1 := strconv.Atoi(season)
					en, e2 := strconv.Atoi(episode)
					if e1 != nil || e2 != nil || sn < 0 || en < 1 {
						return nil, ErrFormat
					}
					play.Season = &sn
					play.Episode = &en
				}
				sourceID := get("history_id", "play_id")
				if sourceID == "" {
					sourceID = fmt.Sprintf("%x", sha256.Sum256([]byte(key+"|"+at.UTC().Format("2006-01-02T15:04:05.999999999Z07:00")+"|"+season+"|"+episode)))
				}
				play.ExternalID = "wetrakr:" + sourceID
				duplicate := false
				for _, p := range rec.Plays {
					duplicate = duplicate || p.ExternalID == play.ExternalID
				}
				if !duplicate {
					rec.Plays = append(rec.Plays, play)
				}
				if rec.Status == "" {
					if kind == "movie" {
						rec.Status = "completed"
					} else {
						rec.Status = "in_progress"
					}
				}
			}
		}
	}
	return merged.records(), nil
}
