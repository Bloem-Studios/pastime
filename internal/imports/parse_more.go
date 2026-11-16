package imports

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Bloem-Studios/pastime/internal/catalog"
	"github.com/Bloem-Studios/pastime/internal/library"
)

// ---- Floppy and Yamtrack CSV export ----
//
// One row per tracked title, season or episode, with the source service and its
// identifier. Episode rows carry the show's identifier plus season and episode numbers,
// so they become plays on the show.

var yamtrackKinds = map[string]string{
	"movie": "movie", "tv": "series", "season": "series", "episode": "series", "anime": "anime", "manga": "manga",
	"game": "game", "book": "book", "audiobook": "audiobook", "comic": "comic", "boardgame": "boardgame", "music": "music", "podcast": "podcast",
	"video": "video",
}

func yamtrackStatus(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "completed":
		return "completed"
	case "in progress":
		return "in_progress"
	case "paused":
		return "paused"
	case "dropped":
		return "dropped"
	case "planning":
		return "planned"
	}
	return ""
}

func score(s string) *int {
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || f <= 0 || f > 10 {
		return nil
	}
	return intp(int(math.Round(f)))
}

// minutes reads "HH:MM" or a plain number of minutes.
func minutes(s string) *int {
	s = strings.TrimSpace(s)
	if h, m, ok := strings.Cut(s, ":"); ok {
		hh, err1 := strconv.Atoi(h)
		mm, err2 := strconv.Atoi(m)
		if err1 != nil || err2 != nil || hh < 0 || mm < 0 {
			return nil
		}
		return intp(hh*60 + mm)
	}
	if n, err := strconv.Atoi(s); err == nil && n >= 0 {
		return &n
	}
	return nil
}

var openLibraryWork = regexp.MustCompile(`^OL\d+W$`)

// identify names a title by its source identifier where this server has that provider,
// and by title otherwise.
func identify(r *Record, source, kind, id, title string) {
	switch {
	case id == "":
	case source == "tmdb" && (kind == "movie" || kind == "series"):
		r.Direct = &DirectRef{Provider: "tmdb", Kind: kind, ID: id}
	case source == "mal" && (kind == "anime" || kind == "manga"):
		r.External = &ExternalRef{Provider: "anilist", Kind: kind, Scheme: "mal", Value: id}
	case source == "igdb" && kind == "game":
		r.Direct = &DirectRef{Provider: "igdb", Kind: kind, ID: id}
	case source == "openlibrary" && openLibraryWork.MatchString(id):
		r.Direct = &DirectRef{Provider: "openlibrary", Kind: kind, ID: id}
	case source == "bgg" && kind == "boardgame":
		r.Direct = &DirectRef{Provider: "bgg", Kind: kind, ID: id}
	case source == "comicvine" && kind == "comic":
		r.Direct = &DirectRef{Provider: "comicvine", Kind: kind, ID: id}
	case source == "musicbrainz" && kind == "music":
		r.Direct = &DirectRef{Provider: "musicbrainz", Kind: kind, ID: id}
	}
	if title != "" {
		r.Title = &catalog.TitleReference{Kind: kind, Title: title}
	}
}

func ParseYamtrack(data []byte) ([]Record, error) {
	rows, err := csvRows(data)
	if err != nil {
		return nil, err
	}
	if len(rows) > 0 {
		if _, ok := rows[0]["media_type"]; !ok {
			return nil, fmt.Errorf("%w: expected Floppy or Yamtrack columns media_id, source and media_type", ErrFormat)
		}
	}
	var m merger
	for _, row := range rows {
		if t := strings.ToLower(row["row_type"]); t != "" && t != "media" {
			continue // lists, collection and tag rows
		}
		mediaType := strings.ToLower(row["media_type"])
		kind, ok := yamtrackKinds[mediaType]
		if !ok {
			continue
		}
		source, id, title := strings.ToLower(row["source"]), row["media_id"], row["title"]
		if id == "" && title == "" {
			continue
		}
		key := kind + "|" + source + "|" + id
		if id == "" {
			key = kind + "|title|" + strings.ToLower(title)
		}
		r := m.get(key, func() Record {
			r := Record{Label: title, Kind: kind}
			identify(&r, source, kind, id, title)
			if r.Direct == nil && r.External == nil && r.Title == nil {
				r.Label = mediaType + " " + id
			}
			return r
		})
		switch mediaType {
		case "episode":
			at, ok := parseDate(row["end_date"])
			season, err1 := strconv.Atoi(row["season_number"])
			episode, err2 := strconv.Atoi(row["episode_number"])
			if ok && err1 == nil && err2 == nil {
				r.Plays = append(r.Plays, PlayRef{Season: &season, Episode: &episode, At: at, ExternalID: fmt.Sprintf("yamtrack:%s:%s:%d:%d:%s", source, id, season, episode, row["end_date"])})
			}
			continue
		case "season":
			continue // a season's status follows from its episodes
		}
		if title != "" && r.Label != title {
			r.Label = title
		}
		r.Status = yamtrackStatus(row["status"])
		r.Rating = score(row["score"])
		r.Notes = row["notes"]
		switch kind {
		case "anime", "manga", "book", "comic":
			if n, err := strconv.Atoi(row["progress"]); err == nil && n > 0 {
				r.Progress = &n
			}
		case "game":
			r.Progress = minutes(row["progress"])
		}
		if at, ok := parseDate(row["end_date"]); ok && r.Status == "completed" && kind != "series" {
			r.Plays = append(r.Plays, PlayRef{At: at, ExternalID: "yamtrack:" + key + ":" + row["end_date"]})
		}
	}
	return m.records(), nil
}

// ---- Grouvee (JSON export, optionally zipped) ----

type grouveeEntry struct {
	Name    string         `json:"name"`
	IGDB    json.Number    `json:"igdb_id"`
	Shelves map[string]any `json:"shelves"`
	Rating  *float64       `json:"rating"`
	Review  string         `json:"review"`
	Dates   []grouveeDate  `json:"dates"`
}

type grouveeDate struct {
	Started  string `json:"date_started"`
	Finished string `json:"date_finished"`
	Seconds  int64  `json:"seconds_played"`
	Notes    string `json:"notes"`
}

func ParseGrouvee(data []byte) ([]Record, error) {
	if isZip(data) {
		files, err := readZip(data)
		if err != nil {
			return nil, err
		}
		data = nil
		for name, b := range files {
			if strings.HasSuffix(strings.ToLower(name), ".json") {
				data = b
				break
			}
		}
		if data == nil {
			return nil, fmt.Errorf("%w: no JSON file in the archive", ErrFormat)
		}
	}
	var export struct {
		Collection []grouveeEntry `json:"collection"`
	}
	if err := json.Unmarshal(data, &export); err != nil || export.Collection == nil {
		return nil, fmt.Errorf("%w: expected a Grouvee JSON export", ErrFormat)
	}
	var out []Record
	for _, e := range export.Collection {
		name := strings.TrimSpace(e.Name)
		if name == "" {
			continue
		}
		r := Record{Label: name, Kind: "game", Title: &catalog.TitleReference{Kind: "game", Title: name}}
		if id := e.IGDB.String(); id != "" && id != "0" {
			r.Direct = &DirectRef{Provider: "igdb", Kind: "game", ID: id}
		}
		// Several shelves can apply at once; the most advanced one wins.
		for _, shelf := range []struct{ name, status string }{{"Playing", "in_progress"}, {"Played", "completed"}, {"Backlog", "planned"}, {"Wish List", "planned"}} {
			if _, ok := e.Shelves[shelf.name]; ok {
				r.Status = shelf.status
				break
			}
		}
		if e.Rating != nil && *e.Rating > 0 {
			r.Rating = intp(int(math.Round(math.Min(*e.Rating, 5) * 2)))
		}
		var notes []string
		var seconds int64
		for i, d := range e.Dates {
			seconds += d.Seconds
			if n := strings.TrimSpace(d.Notes); n != "" {
				notes = append(notes, n)
			}
			if at, ok := parseDate(d.Finished); ok {
				r.Plays = append(r.Plays, PlayRef{At: at, ExternalID: fmt.Sprintf("grouvee:%s:%s:%d", e.IGDB.String(), name, i)})
			}
		}
		if seconds > 0 {
			r.Progress = intp(int(seconds / 60))
		}
		if review := strings.TrimSpace(e.Review); review != "" {
			notes = append(notes, review)
		}
		r.Notes = strings.Join(notes, "\n\n")
		out = append(out, r)
	}
	return out, nil
}

// ---- TV Time (the GDPR data export) ----
//
// tracking-prod-records.csv holds watched episodes and tracking-prod-records-v2.csv
// watched films. Either file, or the whole archive, can be uploaded.

func ParseTVTime(data []byte) ([]Record, error) {
	var files [][]byte
	if isZip(data) {
		all, err := readZip(data)
		if err != nil {
			return nil, err
		}
		for _, n := range []string{"tracking-prod-records.csv", "tracking-prod-records-v2.csv"} {
			if b, ok := all[n]; ok {
				files = append(files, b)
			}
		}
		if len(files) == 0 {
			return nil, fmt.Errorf("%w: no tracking-prod-records CSV in the archive", ErrFormat)
		}
	} else {
		files = [][]byte{data}
	}
	var m merger
	recognized := false
	for _, b := range files {
		rows, err := csvRows(b)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			if show := row["series_name"]; show != "" {
				recognized = true
				season, err1 := strconv.Atoi(row["season_number"])
				episode, err2 := strconv.Atoi(row["episode_number"])
				at, ok := parseDate(row["created_at"])
				r := m.get("show:"+strings.ToLower(show), func() Record {
					return Record{Label: show, Kind: "series", Video: &catalog.Reference{Kind: "series", ShowTitle: show}}
				})
				if err1 == nil && err2 == nil && ok {
					r.Plays = append(r.Plays, PlayRef{Season: &season, Episode: &episode, At: at, ExternalID: fmt.Sprintf("tvtime:%s:%d:%d:%s", strings.ToLower(show), season, episode, row["created_at"])})
				}
				continue
			}
			if film := row["movie_name"]; film != "" {
				recognized = true
				if strings.ToLower(row["type"]) != "watch" {
					continue
				}
				r := m.get("movie:"+strings.ToLower(film), func() Record {
					return Record{Label: film, Kind: "movie", Video: &catalog.Reference{Kind: "movie", Title: film}}
				})
				if at, ok := parseDate(row["updated_at"]); ok {
					r.Plays = append(r.Plays, PlayRef{At: at, ExternalID: "tvtime:movie:" + strings.ToLower(film) + ":" + row["updated_at"]})
				}
			}
		}
	}
	if !recognized {
		return nil, fmt.Errorf("%w: expected TV Time columns series_name or movie_name", ErrFormat)
	}
	return m.records(), nil
}

// ---- Any CSV with recognizable column names ----

var genericColumns = map[string][]string{
	"title":    {"title", "name", "movie", "show", "book", "game"},
	"kind":     {"type", "kind", "media_type", "category"},
	"year":     {"year", "release_year"},
	"status":   {"status", "state", "shelf"},
	"rating":   {"rating", "score", "my_rating", "rating10"},
	"stars":    {"stars", "star_rating"},
	"date":     {"date", "watched_at", "watched", "finished", "finished_at", "completed_at", "date_read", "played_at"},
	"tmdb":     {"tmdb", "tmdb_id"},
	"imdb":     {"imdb", "imdb_id", "const"},
	"isbn":     {"isbn", "isbn13", "isbn_13"},
	"creator":  {"author", "authors", "creator", "artist", "developer"},
	"season":   {"season", "season_number"},
	"episode":  {"episode", "episode_number"},
	"notes":    {"notes", "note", "review", "comment"},
	"progress": {"progress"},
}

var genericKinds = map[string]string{
	"movie": "movie", "film": "movie", "tv": "series", "show": "series", "series": "series", "tv show": "series", "episode": "series",
	"anime": "anime", "manga": "manga", "book": "book", "ebook": "book", "audiobook": "audiobook", "comic": "comic", "game": "game",
	"video game": "game", "board game": "boardgame", "boardgame": "boardgame", "album": "music", "music": "music", "podcast": "podcast",
	"video": "video", "youtube": "video",
}

func genericStatus(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	switch s {
	case "completed", "complete", "watched", "read", "played", "finished", "done", "seen":
		return "completed"
	case "in progress", "in_progress", "watching", "reading", "playing", "listening", "currently-reading":
		return "in_progress"
	case "paused", "on hold", "on_hold":
		return "paused"
	case "dropped", "abandoned", "dnf", "did not finish":
		return "dropped"
	case "planned", "planning", "plan to watch", "want to read", "to-read", "watchlist", "backlog", "wishlist":
		return "planned"
	}
	return ""
}

func ParseGenericCSV(data []byte) ([]Record, error) {
	rows, err := csvRows(data)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	col := map[string]string{}
	for name := range rows[0] {
		norm := strings.ReplaceAll(strings.ToLower(strings.TrimSpace(name)), " ", "_")
		for field, aliases := range genericColumns {
			for _, a := range aliases {
				if norm == strings.ReplaceAll(a, " ", "_") && col[field] == "" {
					col[field] = name
				}
			}
		}
	}
	if col["title"] == "" && col["tmdb"] == "" && col["imdb"] == "" && col["isbn"] == "" {
		return nil, fmt.Errorf("%w: the CSV needs a title, tmdb_id, imdb_id or isbn column", ErrFormat)
	}
	get := func(row map[string]string, field string) string { return row[col[field]] }
	var m merger
	for _, row := range rows {
		title, imdb, tmdb, isbn := get(row, "title"), get(row, "imdb"), get(row, "tmdb"), cleanISBN(get(row, "isbn"))
		kind := genericKinds[strings.ToLower(get(row, "kind"))]
		season, errS := strconv.Atoi(get(row, "season"))
		episode, errE := strconv.Atoi(get(row, "episode"))
		isEpisode := errS == nil && errE == nil
		switch {
		case kind != "":
		case isEpisode:
			kind = "series"
		case isbn != "":
			kind = "book"
		default:
			kind = "movie"
		}
		if title == "" && imdb == "" && tmdb == "" && isbn == "" {
			continue
		}
		yr := year(get(row, "year"))
		key := strings.Join([]string{kind, strings.ToLower(title), get(row, "year"), imdb, tmdb, isbn}, "|")
		r := m.get(key, func() Record {
			label := title
			if label == "" {
				label = strings.TrimSpace(imdb + " " + tmdb + " " + isbn)
			}
			r := Record{Label: label, Kind: kind}
			switch kind {
			case "movie":
				r.Video = &catalog.Reference{Kind: "movie", TMDB: tmdb, IMDB: imdb, Title: title, Year: yr}
			case "series":
				r.Video = &catalog.Reference{Kind: "series", ShowTMDB: tmdb, ShowIMDB: imdb, ShowTitle: title, Year: yr}
			case "book", "audiobook":
				r.Book = &catalog.BookReference{ISBN: isbn, Title: title, Author: strings.Split(get(row, "creator"), ",")[0], Kind: kind}
			default:
				r.Title = &catalog.TitleReference{Kind: kind, Title: title, Creator: get(row, "creator"), Year: yr}
			}
			return r
		})
		if st := genericStatus(get(row, "status")); st != "" {
			r.Status = st
		}
		if rt := score(get(row, "rating")); rt != nil {
			r.Rating = rt
		} else if st := stars(get(row, "stars")); st != nil {
			r.Rating = st
		}
		if n := get(row, "notes"); n != "" {
			r.Notes = n
		}
		if n, err := strconv.Atoi(get(row, "progress")); err == nil && n > 0 && !isEpisode {
			r.Progress = &n
		}
		if at, ok := parseDate(get(row, "date")); ok {
			p := PlayRef{At: at, ExternalID: "csv:" + key + ":" + at.Format(time.RFC3339)}
			if isEpisode {
				p.Season, p.Episode = &season, &episode
				p.ExternalID += fmt.Sprintf(":%d:%d", season, episode)
			}
			r.Plays = append(r.Plays, p)
		}
	}
	return m.records(), nil
}

// ---- CLZ (Collectorz) collection export ----
//
// People choose which columns CLZ exports, so columns are matched by name: known ones
// identify the title and describe the copy, and every other filled column becomes a
// custom collection field on the copy. Wish-list rows are planned titles without a copy.

var clzStructural = map[string]bool{
	"title": true, "sorttitle": true, "originaltitle": true, "series": true, "issuenr": true, "issuenumber": true, "volume": true, "year": true,
	"releaseyear": true, "publicationyear": true, "releasedate": true, "publisher": true, "platform": true, "barcode": true, "upc": true, "ean": true, "isbn": true,
	"imdbnumber": true, "imdb": true, "imdburl": true, "tmdb": true, "quantity": true, "qty": true, "collectionstatus": true, "status": true, "purchasedate": true,
	"index": true, "id": true, "clzid": true, "format": true, "media": true, "purchaseprice": true, "purchasestore": true, "store": true, "edition": true,
	"author": true, "authors": true, "artist": true, "notes": true, "location": true, "storagedevice": true, "myrating": true, "rating": true, "read": true, "seen": true, "completed": true,
}

func clzKey(label string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			return r
		case r >= 'A' && r <= 'Z':
			return r + 32
		}
		return -1
	}, label)
}

func clzFormat(kind, raw string) string {
	f := strings.ToLower(raw)
	switch {
	case strings.Contains(f, "4k") || strings.Contains(f, "uhd"):
		return "uhd_bluray"
	case strings.Contains(f, "blu"):
		return "bluray"
	case strings.Contains(f, "dvd"):
		return "dvd"
	case strings.Contains(f, "vhs"):
		return "vhs"
	case strings.Contains(f, "vinyl") || strings.Contains(f, "lp"):
		return "vinyl"
	case strings.Contains(f, "cassette"):
		return "cassette"
	case f == "cd" || strings.Contains(f, "compact disc"):
		return "cd"
	case strings.Contains(f, "hardcover") || strings.Contains(f, "hardback"):
		return "hardcover"
	case strings.Contains(f, "paperback") || strings.Contains(f, "softcover") || strings.Contains(f, "trade"):
		return "paperback"
	case strings.Contains(f, "ebook") || strings.Contains(f, "e-book") || strings.Contains(f, "kindle"):
		return "ebook"
	case strings.Contains(f, "audio"):
		return "audiobook"
	case strings.Contains(f, "digital") || strings.Contains(f, "download") || strings.Contains(f, "steam"):
		if kind == "game" {
			return "game_digital"
		}
		return "digital"
	case strings.Contains(f, "box"):
		return "boxed"
	}
	switch kind {
	case "game":
		return "game_physical"
	case "book":
		return "paperback"
	case "music":
		return "cd"
	case "boardgame":
		return "boxed"
	}
	return "other"
}

var imdbID = regexp.MustCompile(`tt\d{1,10}`)

// clzPrice reads "$19.99", "19,99 €" or "EUR 19.99" into cents and a currency code.
func clzPrice(raw string) (*int, string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, ""
	}
	currency := ""
	for sym, code := range map[string]string{"$": "USD", "€": "EUR", "£": "GBP", "¥": "JPY"} {
		if strings.Contains(raw, sym) {
			currency = code
		}
	}
	for _, code := range []string{"USD", "EUR", "GBP", "CAD", "AUD", "JPY", "CHF", "SEK", "NOK", "DKK"} {
		if strings.Contains(strings.ToUpper(raw), code) {
			currency = code
		}
	}
	num := strings.Map(func(r rune) rune {
		if (r >= '0' && r <= '9') || r == '.' || r == ',' {
			return r
		}
		return -1
	}, raw)
	if i := strings.LastIndexAny(num, ".,"); i >= 0 && len(num)-i-1 <= 2 {
		num = strings.NewReplacer(",", "", ".", "").Replace(num[:i]) + "." + num[i+1:]
	} else {
		num = strings.NewReplacer(",", "", ".", "").Replace(num)
	}
	f, err := strconv.ParseFloat(num, 64)
	if err != nil || f < 0 || f > 1000000 {
		return nil, ""
	}
	cents := int(math.Round(f * 100))
	return &cents, currency
}

func clzDate(raw string) *string {
	raw = strings.TrimSpace(raw)
	for _, layout := range []string{"2006-01-02", "2006/01/02", "Jan 2, 2006", "January 2, 2006", "02-01-2006"} {
		if t, err := time.Parse(layout, raw); err == nil {
			s := t.Format("2006-01-02")
			return &s
		}
	}
	return nil
}

func ParseCLZ(data []byte) ([]Record, error) {
	rows, err := csvRows(data)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	keys := map[string]string{} // normalized key -> header
	for header := range rows[0] {
		if k := clzKey(header); k != "" {
			if _, taken := keys[k]; !taken {
				keys[k] = header
			}
		}
	}
	get := func(row map[string]string, names ...string) string {
		for _, n := range names {
			if h, ok := keys[n]; ok && strings.TrimSpace(row[h]) != "" {
				return strings.TrimSpace(row[h])
			}
		}
		return ""
	}
	if _, ok := keys["title"]; !ok {
		return nil, fmt.Errorf("%w: expected a CLZ export with a Title column", ErrFormat)
	}
	// The product decides the media type; its telltale columns say which one this is.
	kind := "movie"
	switch {
	case keys["isbn"] != "" || keys["author"] != "" || keys["authors"] != "":
		kind = "book"
	case keys["issuenr"] != "" || keys["issuenumber"] != "":
		kind = "comic"
	case keys["platform"] != "":
		kind = "game"
	case keys["artist"] != "":
		kind = "music"
	}
	var m merger
	for i, row := range rows {
		title := get(row, "title")
		if title == "" {
			continue
		}
		yr := year(get(row, "year", "releaseyear", "publicationyear"))
		if yr == nil {
			if d := clzDate(get(row, "releasedate")); d != nil {
				yr = year((*d)[:4])
			}
		}
		imdb := imdbID.FindString(get(row, "imdbnumber", "imdb", "imdburl"))
		isbn := cleanISBN(get(row, "isbn"))
		creator := get(row, "author", "authors", "artist")
		series := get(row, "series")
		key := strings.Join([]string{kind, strings.ToLower(title), fmt.Sprint(derefInt(yr)), imdb, isbn, strings.ToLower(series)}, "|")
		r := m.get(key, func() Record {
			r := Record{Label: title, Kind: kind, Status: "planned"}
			switch kind {
			case "movie":
				r.Video = &catalog.Reference{Kind: "movie", IMDB: imdb, TMDB: get(row, "tmdb"), Title: title, Year: yr}
			case "book":
				r.Book = &catalog.BookReference{ISBN: isbn, Title: title, Author: strings.Split(creator, ",")[0], Kind: "book"}
			case "comic":
				name := series
				if name == "" {
					name = title
				}
				r.Label = name
				r.Title = &catalog.TitleReference{Kind: "comic", Title: name, Year: yr}
			default:
				r.Title = &catalog.TitleReference{Kind: kind, Title: title, Creator: creator, Year: yr}
			}
			return r
		})
		status := strings.ToLower(get(row, "collectionstatus", "status"))
		if strings.Contains(status, "wish") || strings.Contains(status, "want") || strings.Contains(status, "not in collection") || strings.Contains(status, "on order") {
			continue // A wished-for title is planned and owned by nobody yet.
		}
		switch strings.ToLower(get(row, "read", "seen", "completed")) {
		case "yes", "true", "1", "x":
			r.Status = "completed"
		}
		quantity := 1
		if n, err := strconv.Atoi(get(row, "quantity", "qty")); err == nil && n > 1 && n <= 20 {
			quantity = n
		}
		id := get(row, "clzid", "id", "index", "barcode", "upc", "ean")
		if id == "" {
			id = strconv.Itoa(i) + ":" + strings.ToLower(title)
		}
		price, currency := clzPrice(get(row, "purchaseprice"))
		custom := map[string]string{}
		for k, header := range keys {
			if !clzStructural[k] && strings.TrimSpace(row[header]) != "" {
				custom[strings.TrimSpace(header)] = strings.TrimSpace(row[header])
			}
		}
		for _, extra := range []string{"barcode", "upc", "ean", "platform"} {
			if v := get(row, extra); v != "" {
				custom[keys[extra]] = v
			}
		}
		for n := 0; n < quantity; n++ {
			r.Copies = append(r.Copies, library.ImportCopy{
				Format: clzFormat(kind, get(row, "format", "media")), Edition: get(row, "edition"), Store: get(row, "purchasestore", "store"),
				Location: get(row, "location", "storagedevice"), Notes: get(row, "notes"), AcquiredOn: clzDate(get(row, "purchasedate")),
				PriceCents: price, Currency: currency, Custom: custom, ExternalID: "clz:" + id + ":" + strconv.Itoa(n),
			})
		}
	}
	return m.records(), nil
}

func derefInt(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}
