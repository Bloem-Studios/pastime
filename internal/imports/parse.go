// Package imports turns other services' exports into library writes. Parsers produce
// Records with whatever identifiers the source has; the runner matches them to the
// catalog and applies them idempotently.
package imports

import (
	"archive/zip"
	"bufio"
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Bloem-Studios/pastime/internal/catalog"
	"github.com/Bloem-Studios/pastime/internal/library"
)

// Record is one title from an import source.
type Record struct {
	ScoredAt *time.Time
	Lists    []string
	Label    string // shown in the unmatched list
	Kind     string // movie, series, anime, manga, book, audiobook, or any catalog kind for native imports

	Video    *catalog.Reference      // movies and shows
	Book     *catalog.BookReference  // books
	Direct   *DirectRef              // provider identity already known
	External *ExternalRef            // another service's identifier, looked up on a provider
	Title    *catalog.TitleReference // title, year and creator only

	Status   string
	Progress *int
	// Fraction is progress as a share of the whole (0-1), converted with the title's total.
	Fraction *float64
	Rating   *int
	Notes    string
	Plays    []PlayRef
	Copies   []library.ImportCopy // owned copies from a collection export
}

// DirectRef names a catalog record by provider identity.
type DirectRef struct{ Provider, Kind, ID string }

// ExternalRef names a record by another service's identifier, such as a MyAnimeList id
// looked up on AniList. Title is tried when the identifier finds nothing.
type ExternalRef struct{ Provider, Kind, Scheme, Value string }

// PlayRef is a dated play of the title or, with Season/Episode or ChildID, of one part.
type PlayRef struct {
	Season, Episode *int
	TrackNumber     *int
	ChildID         string // provider id of a child record (podcast episode, issue)
	ChildTitle      string // a child's title, tried when there is no usable id
	At              time.Time
	ExternalID      string
	Minutes         *int           // time spent, when the source records it
	Details         map[string]any // board game players and location
}

var ErrFormat = errors.New("unrecognized import file")

const maxUncompressed = 64 << 20

func intp(n int) *int { return &n }

func readZip(data []byte, preservePaths ...bool) (map[string][]byte, error) {
	z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("%w: the archive could not be opened", ErrFormat)
	}
	out := map[string][]byte{}
	var total int64
	for _, f := range z.File {
		if f.FileInfo().IsDir() {
			continue
		}
		if f.UncompressedSize64 > uint64(maxUncompressed-total) {
			return nil, fmt.Errorf("%w: archive expands beyond 64 MiB", ErrFormat)
		}
		name := path.Base(f.Name)
		if len(preservePaths) > 0 && preservePaths[0] {
			name = path.Clean(f.Name)
			if path.IsAbs(name) || name == ".." || strings.HasPrefix(name, "../") {
				return nil, fmt.Errorf("%w: invalid archive path", ErrFormat)
			}
		}
		if _, exists := out[name]; exists {
			return nil, fmt.Errorf("%w: archive contains duplicate file names", ErrFormat)
		}
		if len(out) >= 1000 {
			return nil, fmt.Errorf("%w: archive contains too many files", ErrFormat)
		}
		r, err := f.Open()
		if err != nil {
			return nil, fmt.Errorf("%w: %s could not be read", ErrFormat, f.Name)
		}
		b, err := io.ReadAll(io.LimitReader(r, maxUncompressed-total+1))
		r.Close()
		if err != nil {
			return nil, fmt.Errorf("%w: %s could not be read", ErrFormat, f.Name)
		}
		total += int64(len(b))
		if total > maxUncompressed {
			return nil, fmt.Errorf("%w: archive expands beyond 64 MiB", ErrFormat)
		}
		out[name] = b
	}
	return out, nil
}

func isZip(data []byte) bool { return len(data) > 4 && string(data[:4]) == "PK\x03\x04" }

// csvRows reads a header-keyed CSV, tolerating a byte-order mark and ragged rows.
func csvRows(data []byte) ([]map[string]string, error) {
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	r := csv.NewReader(bytes.NewReader(data))
	r.FieldsPerRecord = -1
	r.LazyQuotes = true
	header, err := r.Read()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrFormat, err)
	}
	var rows []map[string]string
	for {
		rec, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrFormat, err)
		}
		row := map[string]string{}
		for i, h := range header {
			if i < len(rec) {
				row[strings.TrimSpace(h)] = strings.TrimSpace(rec[i])
			}
		}
		if len(rows) >= 100000 {
			return nil, fmt.Errorf("%w: CSV exceeds 100,000 records", ErrFormat)
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func parseDate(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.000Z", "2006-01-02 15:04:05.999999999Z07:00", "2006-01-02 15:04:05.999999999", "2006-01-02 15:04:05", "2006/01/02", "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			if len(s) <= 10 {
				// Date-only values are recorded at midday UTC so they stay on the same calendar day.
				t = t.Add(12 * time.Hour)
			}
			return t.UTC(), true
		}
	}
	return time.Time{}, false
}

// stars converts a 0.5-5 star rating to the 0-10 scale; zero means unrated.
func stars(s string) *int {
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || f <= 0 {
		return nil
	}
	return intp(int(math.Round(math.Min(f, 5) * 2)))
}

func year(s string) *int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n < 1800 || n > 3000 {
		return nil
	}
	return &n
}

// merge keeps one record per identity, so ratings, watchlists and plays from separate
// files land on the same title.
type merger struct {
	order []string
	byKey map[string]*Record
}

func (m *merger) get(key string, init func() Record) *Record {
	if m.byKey == nil {
		m.byKey = map[string]*Record{}
	}
	if r, ok := m.byKey[key]; ok {
		return r
	}
	r := init()
	m.byKey[key] = &r
	m.order = append(m.order, key)
	return &r
}

func (m *merger) records() []Record {
	out := make([]Record, 0, len(m.order))
	for _, k := range m.order {
		out = append(out, *m.byKey[k])
	}
	return out
}

// ---- Trakt data export (zip of JSON files shaped like the Trakt API) ----

type traktIDs struct {
	Trakt int64  `json:"trakt"`
	IMDB  string `json:"imdb"`
	TMDB  int64  `json:"tmdb"`
	TVDB  int64  `json:"tvdb"`
}
type traktWork struct {
	Title string   `json:"title"`
	Year  int      `json:"year"`
	IDs   traktIDs `json:"ids"`
}
type traktEntry struct {
	ID        int64      `json:"id"`
	WatchedAt string     `json:"watched_at"`
	RatedAt   string     `json:"rated_at"`
	Rating    int        `json:"rating"`
	Type      string     `json:"type"`
	Movie     *traktWork `json:"movie"`
	Show      *traktWork `json:"show"`
	Episode   *struct {
		Season int `json:"season"`
		Number int `json:"number"`
	} `json:"episode"`
}

func idString(n int64) string {
	if n <= 0 {
		return ""
	}
	return strconv.FormatInt(n, 10)
}

func ParseTrakt(data []byte) ([]Record, error) {
	if !isZip(data) {
		return nil, fmt.Errorf("%w: upload the .zip file from Trakt's data export", ErrFormat)
	}
	files, err := readZip(data)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	var sections []traktSection
	for _, name := range names {
		kind := ""
		switch {
		case strings.HasPrefix(name, "watched-history"):
			kind = "history"
		case strings.HasPrefix(name, "ratings-movies"), strings.HasPrefix(name, "ratings-shows"):
			kind = "ratings"
		case strings.HasPrefix(name, "lists-watchlist"), strings.HasPrefix(name, "watchlist"):
			kind = "watchlist"
		default:
			continue
		}
		var entries []traktEntry
		if err := json.Unmarshal(files[name], &entries); err != nil {
			return nil, fmt.Errorf("%w: %s is not a Trakt export file", ErrFormat, name)
		}
		sections = append(sections, traktSection{kind, entries})
	}
	found := len(sections) > 0
	if !found {
		return nil, fmt.Errorf("%w: no watched-history, ratings or watchlist files in the archive", ErrFormat)
	}
	return fromTrakt(sections), nil
}

// traktSection is one Trakt collection: history, ratings or watchlist. The export files
// and the API return the same shapes.
type traktSection struct {
	kind    string
	entries []traktEntry
}

func fromTrakt(sections []traktSection) []Record {
	var m merger
	movie := func(w *traktWork) *Record {
		key := "movie:" + idString(w.IDs.Trakt) + ":" + w.IDs.IMDB + ":" + idString(w.IDs.TMDB)
		return m.get(key, func() Record {
			return Record{Label: fmt.Sprintf("%s (%d)", w.Title, w.Year), Kind: "movie", Video: &catalog.Reference{Kind: "movie", TMDB: idString(w.IDs.TMDB), IMDB: w.IDs.IMDB, Title: w.Title, Year: year(strconv.Itoa(w.Year))}}
		})
	}
	show := func(w *traktWork) *Record {
		key := "show:" + idString(w.IDs.Trakt) + ":" + idString(w.IDs.TMDB) + ":" + idString(w.IDs.TVDB)
		return m.get(key, func() Record {
			return Record{Label: w.Title, Kind: "series", Video: &catalog.Reference{Kind: "series", ShowTMDB: idString(w.IDs.TMDB), ShowIMDB: w.IDs.IMDB, ShowTVDB: idString(w.IDs.TVDB), ShowTitle: w.Title}}
		})
	}
	for _, section := range sections {
		for _, e := range section.entries {
			var r *Record
			switch {
			case e.Movie != nil:
				r = movie(e.Movie)
			case e.Show != nil:
				r = show(e.Show)
			default:
				continue
			}
			switch section.kind {
			case "history":
				at, ok := parseDate(e.WatchedAt)
				if !ok {
					continue
				}
				p := PlayRef{At: at, ExternalID: "history:" + strconv.FormatInt(e.ID, 10)}
				if e.Episode != nil {
					p.Season, p.Episode = intp(e.Episode.Season), intp(e.Episode.Number)
				}
				r.Plays = append(r.Plays, p)
			case "ratings":
				if e.Rating >= 1 && e.Rating <= 10 {
					r.Rating = intp(e.Rating)
					if at, ok := parseDate(e.RatedAt); ok {
						r.ScoredAt = &at
					}
				}
			case "watchlist":
				if r.Status == "" {
					r.Status = "planned"
				}
			}
		}
	}
	return m.records()
}

// ---- Letterboxd (export zip or any of its CSV files) ----

func ParseLetterboxd(data []byte) ([]Record, error) {
	files := map[string][]byte{}
	if isZip(data) {
		all, err := readZip(data)
		if err != nil {
			return nil, err
		}
		for _, n := range []string{"diary.csv", "watched.csv", "ratings.csv", "watchlist.csv"} {
			if b, ok := all[n]; ok {
				files[n] = b
			}
		}
	} else {
		files["upload.csv"] = data
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("%w: no diary, watched, ratings or watchlist CSV in the archive", ErrFormat)
	}
	var m merger
	for _, name := range []string{"watchlist.csv", "watched.csv", "ratings.csv", "diary.csv", "upload.csv"} {
		b, ok := files[name]
		if !ok {
			continue
		}
		rows, err := csvRows(b)
		if err != nil {
			return nil, err
		}
		for i, row := range rows {
			title := row["Name"]
			if title == "" {
				if name == "upload.csv" && i == 0 {
					return nil, fmt.Errorf("%w: expected Letterboxd columns Name and Year", ErrFormat)
				}
				continue
			}
			uri := row["Letterboxd URI"]
			key := strings.ToLower(title) + "|" + row["Year"]
			r := m.get(key, func() Record {
				return Record{Label: fmt.Sprintf("%s (%s)", title, row["Year"]), Kind: "movie", Video: &catalog.Reference{Kind: "movie", Title: title, Year: year(row["Year"])}}
			})
			switch {
			case name == "watchlist.csv":
				if r.Status == "" {
					r.Status = "planned"
				}
			case row["Watched Date"] != "":
				// A diary row is one viewing.
				if at, ok := parseDate(row["Watched Date"]); ok {
					r.Plays = append(r.Plays, PlayRef{At: at, ExternalID: "diary:" + uri + ":" + row["Watched Date"] + ":" + strconv.Itoa(i)})
				}
				r.Status = "completed"
			default:
				r.Status = "completed"
			}
			if rating := stars(row["Rating"]); rating != nil {
				r.Rating = rating
			}
		}
	}
	return m.records(), nil
}

// ---- IMDb ratings or watchlist CSV ----

func ParseIMDb(data []byte) ([]Record, error) {
	rows, err := csvRows(data)
	if err != nil {
		return nil, err
	}
	if len(rows) > 0 && rows[0]["Const"] == "" {
		return nil, fmt.Errorf("%w: expected IMDb columns Const and Title Type", ErrFormat)
	}
	var out []Record
	for _, row := range rows {
		id, kind := row["Const"], ""
		// IMDb writes types like "Movie", "TV Series" and "TV Mini Series".
		switch strings.ReplaceAll(strings.ToLower(row["Title Type"]), " ", "") {
		case "movie", "tvmovie", "video", "short", "tvspecial":
			kind = "movie"
		case "tvseries", "tvminiseries":
			kind = "series"
		default:
			continue
		}
		r := Record{Label: fmt.Sprintf("%s (%s)", row["Title"], row["Year"]), Kind: kind}
		if kind == "movie" {
			r.Video = &catalog.Reference{Kind: "movie", IMDB: id, Title: row["Title"], Year: year(row["Year"])}
		} else {
			r.Video = &catalog.Reference{Kind: "series", ShowIMDB: id, ShowTitle: row["Title"]}
		}
		if n, err := strconv.Atoi(row["Your Rating"]); err == nil && n >= 1 && n <= 10 {
			// A rating means it was watched; a watchlist export has no rating column.
			r.Rating, r.Status = intp(n), "completed"
		} else {
			r.Status = "planned"
		}
		out = append(out, r)
	}
	return out, nil
}

// ---- Goodreads and StoryGraph CSV ----

func cleanISBN(s string) string {
	return strings.Trim(strings.TrimSpace(s), `="`)
}

func readStatus(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "read":
		return "completed"
	case "currently-reading", "currently reading":
		return "in_progress"
	case "did-not-finish", "did not finish", "dnf", "abandoned":
		return "dropped"
	case "paused":
		return "paused"
	}
	return "planned"
}

func ParseGoodreads(data []byte) ([]Record, error) {
	rows, err := csvRows(data)
	if err != nil {
		return nil, err
	}
	if len(rows) > 0 && rows[0]["Book Id"] == "" {
		return nil, fmt.Errorf("%w: expected Goodreads columns Book Id and Exclusive Shelf", ErrFormat)
	}
	var out []Record
	for _, row := range rows {
		isbn := cleanISBN(row["ISBN13"])
		if isbn == "" {
			isbn = cleanISBN(row["ISBN"])
		}
		r := Record{Label: row["Title"] + " — " + row["Author"], Kind: "book", Book: &catalog.BookReference{ISBN: isbn, Title: row["Title"], Author: row["Author"], Kind: "book"},
			Status: readStatus(row["Exclusive Shelf"]), Notes: row["Private Notes"]}
		if n, err := strconv.Atoi(row["My Rating"]); err == nil && n > 0 {
			r.Rating = intp(n * 2)
		}
		if at, ok := parseDate(row["Date Read"]); ok && r.Status == "completed" {
			r.Plays = []PlayRef{{At: at, ExternalID: "book:" + row["Book Id"] + ":" + row["Date Read"]}}
		}
		out = append(out, r)
	}
	return out, nil
}

func ParseStoryGraph(data []byte) ([]Record, error) {
	rows, err := csvRows(data)
	if err != nil {
		return nil, err
	}
	if len(rows) > 0 && rows[0]["Read Status"] == "" && rows[0]["Title"] == "" {
		return nil, fmt.Errorf("%w: expected StoryGraph columns Title and Read Status", ErrFormat)
	}
	var out []Record
	for _, row := range rows {
		kind := "book"
		if strings.Contains(strings.ToLower(row["Format"]), "audio") {
			kind = "audiobook"
		}
		isbn := cleanISBN(row["ISBN/UID"])
		r := Record{Label: row["Title"] + " — " + row["Authors"], Kind: kind, Book: &catalog.BookReference{ISBN: isbn, Title: row["Title"], Author: strings.Split(row["Authors"], ",")[0], Kind: kind},
			Status: readStatus(row["Read Status"]), Rating: stars(row["Star Rating"]), Notes: row["Review"]}
		// "Dates Read" holds one or more dates or ranges separated by commas; each is a read.
		for i, part := range strings.Split(row["Dates Read"], ",") {
			part = strings.TrimSpace(part)
			if j := strings.LastIndex(part, "-"); j > 7 && strings.Count(part, "/") >= 4 {
				part = strings.TrimSpace(part[j+1:])
			}
			if at, ok := parseDate(part); ok {
				r.Plays = append(r.Plays, PlayRef{At: at, ExternalID: "book:" + isbn + ":" + row["Title"] + ":" + strconv.Itoa(i) + ":" + part})
			}
		}
		out = append(out, r)
	}
	return out, nil
}

// ---- Native export (NDJSON written by this application) ----

// NativeEntry is one line of the native export.
type NativeEntry struct {
	Type       string       `json:"type"`
	Provider   string       `json:"provider,omitempty"`
	ProviderID string       `json:"provider_id,omitempty"`
	Kind       string       `json:"kind"`
	Title      string       `json:"title"`
	Year       *int         `json:"year,omitempty"`
	Status     string       `json:"status"`
	Progress   int          `json:"progress"`
	Rating     *int         `json:"rating,omitempty"`
	Notes      string       `json:"notes,omitempty"`
	Plays      []NativePlay `json:"plays,omitempty"`
}
type NativePlay struct {
	At      time.Time `json:"at"`
	Season  *int      `json:"season,omitempty"`
	Episode *int      `json:"episode,omitempty"`
	ChildID string    `json:"child_provider_id,omitempty"`
	ID      string    `json:"id"`
}

func ParseNative(data []byte) ([]Record, error) {
	var out []Record
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var e NativeEntry
		if err := json.Unmarshal(line, &e); err != nil {
			return nil, fmt.Errorf("%w: not a Pastime export", ErrFormat)
		}
		if e.Type != "entry" {
			continue
		}
		if e.Provider == "" {
			return nil, fmt.Errorf("%w: this legacy export contains manual entries; create a version 2 native snapshot to preserve them", ErrFormat)
		}
		r := Record{Label: e.Title, Kind: e.Kind, Direct: &DirectRef{Provider: e.Provider, Kind: e.Kind, ID: e.ProviderID}, Status: e.Status, Progress: intp(e.Progress), Rating: e.Rating, Notes: e.Notes}
		for _, p := range e.Plays {
			r.Plays = append(r.Plays, PlayRef{Season: p.Season, Episode: p.Episode, ChildID: p.ChildID, At: p.At, ExternalID: "native:" + p.ID})
		}
		out = append(out, r)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrFormat, err)
	}
	return out, nil
}
