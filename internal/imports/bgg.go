package imports

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Bloem-Studios/pastime/internal/catalog/provider"
	"github.com/Bloem-Studios/pastime/internal/library"
)

// BoardGameGeek's XML API 2 needs an application token, set as BGG_TOKEN. The link
// reads a person's board game collection (owned copies, wishlist, ratings) and their
// logged plays, with length, location, players, scores and winners.

var bggAPI = "https://boardgamegeek.com/xmlapi2/"

// bggPause is the wait between requests that BoardGameGeek asks API users to keep.
var bggPause = 5 * time.Second

const bggMaxPlayPages = 50 // 100 plays a page

type bggCollection struct {
	XMLName xml.Name  `xml:"items"`
	Items   []bggItem `xml:"item"`
}

type bggItem struct {
	ObjectID string `xml:"objectid,attr"`
	Name     string `xml:"name"`
	Status   struct {
		Own        string `xml:"own,attr"`
		PrevOwned  string `xml:"prevowned,attr"`
		Want       string `xml:"want,attr"`
		WantToPlay string `xml:"wanttoplay,attr"`
		WantToBuy  string `xml:"wanttobuy,attr"`
		Wishlist   string `xml:"wishlist,attr"`
		Preordered string `xml:"preordered,attr"`
	} `xml:"status"`
	NumPlays int `xml:"numplays"`
	Stats    struct {
		Rating struct {
			Value string `xml:"value,attr"`
		} `xml:"rating"`
	} `xml:"stats"`
}

type bggPlays struct {
	XMLName xml.Name  `xml:"plays"`
	Total   int       `xml:"total,attr"`
	Plays   []bggPlay `xml:"play"`
}

type bggPlay struct {
	ID       string `xml:"id,attr"`
	Date     string `xml:"date,attr"`
	Quantity int    `xml:"quantity,attr"`
	Length   int    `xml:"length,attr"`
	Location string `xml:"location,attr"`
	Item     struct {
		Name     string `xml:"name,attr"`
		ObjectID string `xml:"objectid,attr"`
		Subtypes []struct {
			Value string `xml:"value,attr"`
		} `xml:"subtypes>subtype"`
	} `xml:"item"`
	Players []struct {
		Name     string `xml:"name,attr"`
		Username string `xml:"username,attr"`
		Score    string `xml:"score,attr"`
		Win      string `xml:"win,attr"`
	} `xml:"players>player"`
}

type bggErrors struct {
	XMLName  xml.Name `xml:"errors"`
	Messages []string `xml:"error>message"`
}

func (s Store) fetchBGG(ctx context.Context, c *http.Client, cfg *LinkConfig) ([]Record, error) {
	if s.Creds.BGGToken == "" {
		return nil, provider.ErrNotConfigured
	}
	user := strings.TrimSpace(cfg.Username)
	if user == "" || len(user) > 64 {
		return nil, fmt.Errorf("%w: enter your BoardGameGeek username", ErrFormat)
	}
	var coll bggCollection
	q := url.Values{"username": {user}, "subtype": {"boardgame"}, "excludesubtype": {"boardgameexpansion"}, "stats": {"1"}}
	if err := s.bggGet(ctx, c, "collection", q, &coll); err != nil {
		return nil, err
	}
	records := map[string]*Record{}
	var order []string
	for _, it := range coll.Items {
		name := strings.TrimSpace(it.Name)
		if it.ObjectID == "" || name == "" {
			continue
		}
		if _, seen := records[it.ObjectID]; seen {
			continue // a game owned twice appears twice; one library entry is enough
		}
		rec := &Record{Label: name, Kind: "boardgame", Direct: &DirectRef{Provider: "bgg", Kind: "boardgame", ID: it.ObjectID}, Status: "planned"}
		if it.NumPlays > 0 {
			rec.Status = "completed"
		}
		if v, err := strconv.ParseFloat(it.Stats.Rating.Value, 64); err == nil && v >= 1 && v <= 10 {
			rec.Rating = intp(int(math.Round(v)))
		}
		if it.Status.Own == "1" {
			rec.Copies = []library.ImportCopy{{Format: "physical", ExternalID: "bgg-own:" + it.ObjectID}}
		}
		records[it.ObjectID] = rec
		order = append(order, it.ObjectID)
	}
	for page := 1; page <= bggMaxPlayPages; page++ {
		if err := sleepCtx(ctx, bggPause); err != nil {
			return nil, err
		}
		var plays bggPlays
		if err := s.bggGet(ctx, c, "plays", url.Values{"username": {user}, "type": {"thing"}, "subtype": {"boardgame"}, "page": {strconv.Itoa(page)}}, &plays); err != nil {
			return nil, err
		}
		for _, p := range plays.Plays {
			addBGGPlay(records, &order, p)
		}
		if len(plays.Plays) == 0 || page*100 >= plays.Total {
			break
		}
	}
	out := make([]Record, 0, len(order))
	for _, id := range order {
		out = append(out, *records[id])
	}
	return out, nil
}

// addBGGPlay attaches one logged play (or several, for a quantity above one) to its
// game, adding the game when the play is all there is of it.
func addBGGPlay(records map[string]*Record, order *[]string, p bggPlay) {
	at, err := time.Parse(time.DateOnly, p.Date)
	if err != nil || p.ID == "" || p.Item.ObjectID == "" || at.Year() < 1900 {
		return
	}
	at = at.Add(12 * time.Hour) // a day without a time; noon keeps it on that day everywhere
	rec, ok := records[p.Item.ObjectID]
	if !ok {
		name := strings.TrimSpace(p.Item.Name)
		if name == "" {
			return
		}
		rec = &Record{Label: name, Kind: "boardgame", Direct: &DirectRef{Provider: "bgg", Kind: "boardgame", ID: p.Item.ObjectID}, Status: "completed"}
		records[p.Item.ObjectID] = rec
		*order = append(*order, p.Item.ObjectID)
	}
	rec.Status = "completed"
	details := map[string]any{}
	var players []map[string]any
	for _, pl := range p.Players {
		name := strings.TrimSpace(pl.Name)
		if name == "" {
			name = strings.TrimSpace(pl.Username)
		}
		if name == "" {
			continue
		}
		seat := map[string]any{"name": name, "winner": pl.Win == "1"}
		if score, err := strconv.ParseFloat(strings.TrimSpace(pl.Score), 64); err == nil && !math.IsInf(score, 0) && !math.IsNaN(score) && math.Abs(score) <= 1e9 {
			seat["score"] = score
		}
		players = append(players, seat)
		if len(players) == 20 {
			break
		}
	}
	if len(players) > 0 {
		details["players"] = players
	}
	if loc := strings.TrimSpace(p.Location); loc != "" {
		if r := []rune(loc); len(r) > 120 {
			loc = string(r[:120])
		}
		details["location"] = loc
	}
	quantity := min(max(p.Quantity, 1), 20)
	var minutes *int
	if p.Length > 0 {
		minutes = intp(max(1, p.Length/quantity))
	}
	for i := range quantity {
		id := "bgg-play:" + p.ID
		if quantity > 1 {
			id += "#" + strconv.Itoa(i+1)
		}
		rec.Plays = append(rec.Plays, PlayRef{At: at, ExternalID: id, Minutes: minutes, Details: details})
	}
}

// bggGet reads one XML API page. BoardGameGeek answers 202 while it prepares a large
// collection; that run then fails as temporary and the next sync reads it.
func (s Store) bggGet(ctx context.Context, c *http.Client, path string, q url.Values, into any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, bggAPI+path+"?"+q.Encode(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+s.Creds.BGGToken)
	req.Header.Set("Accept", "application/xml")
	resp, err := c.Do(req)
	if err != nil {
		return provider.ErrUnavailable
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return provider.ErrUnavailable
	}
	switch {
	case resp.StatusCode == http.StatusAccepted:
		return fmt.Errorf("%w: BoardGameGeek is preparing this collection; it is read on the next sync", provider.ErrUnavailable)
	case resp.StatusCode == http.StatusTooManyRequests:
		return provider.ErrRateLimited
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return provider.ErrNotConfigured
	case resp.StatusCode >= 500:
		return provider.ErrUnavailable
	case resp.StatusCode != http.StatusOK:
		return fmt.Errorf("%w: BoardGameGeek answered %d", ErrFormat, resp.StatusCode)
	}
	var problem bggErrors
	if xml.Unmarshal(body, &problem) == nil && len(problem.Messages) > 0 {
		return fmt.Errorf("%w: BoardGameGeek: %s", ErrFormat, strings.TrimSpace(problem.Messages[0]))
	}
	if err = xml.Unmarshal(body, into); err != nil {
		return fmt.Errorf("%w: BoardGameGeek sent an unreadable answer", ErrFormat)
	}
	return nil
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
