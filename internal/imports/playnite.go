package imports

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/Bloem-Studios/pastime/internal/catalog"
)

// Playnite has no export of its own; library-export extensions write a CSV with one row
// per game per store. Name is required. Completion status is "Completion Status" or
// "CompletionStatus"; playtime is "TimePlayedHours" (decimal hours) or "Time Played"
// (seconds). A Steam row's "Game Id" is its app id, matched the way the Steam link is.

var playniteStatus = map[string]string{
	"plan to play": "planned",
	"played":       "in_progress",
	"playing":      "in_progress",
	"on hold":      "paused",
	"abandoned":    "dropped",
	"beaten":       "completed",
	"completed":    "completed",
}

// playniteRank orders statuses so a game listed by several stores keeps the furthest one.
var playniteRank = map[string]int{"": 0, "planned": 1, "paused": 2, "in_progress": 3, "dropped": 4, "completed": 5}

var playniteFold = regexp.MustCompile(`[\W_]+`)

type playniteGame struct {
	name    string
	minutes int
	status  string
	steamID string
}

// ParsePlaynite reads a Playnite library CSV. Rows for the same game from different
// stores become one record with the longest playtime and the furthest status. Games that
// were never played and are not planned are left out, since a Playnite library lists
// every game a person owns.
func ParsePlaynite(data []byte) ([]Record, error) {
	r := csv.NewReader(bytes.NewReader(bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))))
	r.FieldsPerRecord, r.LazyQuotes = -1, true
	rows, err := r.ReadAll()
	if err != nil || len(rows) == 0 {
		return nil, fmt.Errorf("%w: expected a Playnite library CSV", ErrFormat)
	}
	col := map[string]int{}
	for i, h := range rows[0] {
		col[strings.ToLower(strings.TrimSpace(h))] = i
	}
	cell := func(row []string, names ...string) (string, bool) {
		for _, n := range names {
			if i, ok := col[n]; ok && i < len(row) {
				return strings.TrimSpace(row[i]), true
			}
		}
		return "", false
	}
	if _, ok := col["name"]; !ok {
		return nil, fmt.Errorf("%w: a Playnite CSV needs a Name column", ErrFormat)
	}
	games := map[string]*playniteGame{}
	var order []string
	for _, row := range rows[1:] {
		name, _ := cell(row, "name")
		if name == "" {
			continue
		}
		minutes, err := playniteMinutes(row, cell)
		if err != nil {
			return nil, err
		}
		raw, _ := cell(row, "completion status", "completionstatus")
		status := playniteStatus[strings.ToLower(raw)]
		key := strings.Join(strings.Fields(playniteFold.ReplaceAllString(strings.ToLower(name), " ")), " ")
		g, seen := games[key]
		if !seen {
			g = &playniteGame{name: name}
			games[key] = g
			order = append(order, key)
		}
		g.minutes = max(g.minutes, minutes)
		if playniteRank[status] > playniteRank[g.status] {
			g.status = status
		}
		source, _ := cell(row, "source", "sources", "library")
		id, _ := cell(row, "game id", "gameid")
		if g.steamID == "" && strings.EqualFold(source, "steam") && isDigits(id) {
			g.steamID = id
		}
	}
	var out []Record
	for _, key := range order {
		g := games[key]
		if g.minutes == 0 && g.status != "planned" && g.status != "completed" {
			continue
		}
		status := g.status
		if status == "" {
			status = "in_progress"
		}
		rec := Record{Label: g.name, Kind: "game", Status: status, Title: &catalog.TitleReference{Kind: "game", Title: g.name}}
		if g.minutes > 0 {
			rec.Progress = intp(g.minutes)
		}
		if g.steamID != "" {
			rec.External = &ExternalRef{Provider: "igdb", Kind: "game", Scheme: "steam", Value: g.steamID}
		}
		out = append(out, rec)
	}
	return out, nil
}

func playniteMinutes(row []string, cell func([]string, ...string) (string, bool)) (int, error) {
	if hours, ok := cell(row, "timeplayedhours", "time played hours"); ok {
		if hours == "" {
			return 0, nil
		}
		h, err := strconv.ParseFloat(strings.ReplaceAll(hours, ",", "."), 64)
		if err != nil || h < 0 || math.IsInf(h, 0) || math.IsNaN(h) || h > 1e6 {
			return 0, fmt.Errorf("%w: a Playnite playtime is not a number of hours", ErrFormat)
		}
		return roundMinutes(h * 3600), nil
	}
	if secs, ok := cell(row, "time played", "playtime"); ok && secs != "" {
		s, err := strconv.ParseFloat(secs, 64)
		if err != nil || s < 0 || s > 1e10 {
			return 0, fmt.Errorf("%w: a Playnite playtime is not a number of seconds", ErrFormat)
		}
		return roundMinutes(s), nil
	}
	return 0, nil
}

// roundMinutes turns seconds into minutes, counting any play at all as at least one.
func roundMinutes(seconds float64) int {
	if seconds <= 0 {
		return 0
	}
	return max(1, int(math.Round(seconds/60)))
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
