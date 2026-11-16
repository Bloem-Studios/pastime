package provider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// RSS reads a podcast straight from its feed, for shows missing from directories or
// with more episodes than a directory lists. The provider id is the feed address.
// Feed addresses come from people, so HTTP is the caller's guarded client.
type RSS struct {
	HTTP  *http.Client
	Agent string
}

func (RSS) Name() string    { return "rss" }
func (RSS) Kinds() []string { return []string{"podcast"} }

const maxFeed = 16 << 20

// FeedURL reports whether s is an address this provider can read.
func FeedURL(s string) bool {
	u, err := url.Parse(strings.TrimSpace(s))
	return err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != "" && u.User == nil && len(s) <= 200
}

type rssFeed struct {
	Channel struct {
		Title       string `xml:"title"`
		Description string `xml:"description"`
		Author      string `xml:"http://www.itunes.com/dtds/podcast-1.0.dtd author"`
		Image       struct {
			Href string `xml:"href,attr"`
		} `xml:"http://www.itunes.com/dtds/podcast-1.0.dtd image"`
		PlainImage struct {
			URL string `xml:"url"`
		} `xml:"image"`
		Categories []struct {
			Text string `xml:"text,attr"`
		} `xml:"http://www.itunes.com/dtds/podcast-1.0.dtd category"`
		Items []struct {
			Title       string `xml:"title"`
			GUID        string `xml:"guid"`
			PubDate     string `xml:"pubDate"`
			Description string `xml:"description"`
			Duration    string `xml:"http://www.itunes.com/dtds/podcast-1.0.dtd duration"`
			Episode     string `xml:"http://www.itunes.com/dtds/podcast-1.0.dtd episode"`
			Season      string `xml:"http://www.itunes.com/dtds/podcast-1.0.dtd season"`
			Image       struct {
				Href string `xml:"href,attr"`
			} `xml:"http://www.itunes.com/dtds/podcast-1.0.dtd image"`
			Enclosure struct {
				URL string `xml:"url,attr"`
			} `xml:"enclosure"`
		} `xml:"item"`
	} `xml:"channel"`
}

func (p RSS) Search(ctx context.Context, kind, query string) ([]Record, error) {
	if kind != "podcast" {
		return nil, ErrUnsupported
	}
	if !FeedURL(query) {
		return []Record{}, nil
	}
	r, err := p.Fetch(ctx, kind, strings.TrimSpace(query))
	if errors.Is(err, ErrNotFound) {
		return []Record{}, nil
	}
	if err != nil {
		return nil, err
	}
	r.Children, r.ChildrenLoaded = nil, false
	return []Record{r}, nil
}

func (p RSS) Fetch(ctx context.Context, kind, id string) (Record, error) {
	if kind != "podcast" || !FeedURL(id) || p.HTTP == nil {
		return Record{}, ErrNotFound
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, id, nil)
	if err != nil {
		return Record{}, ErrNotFound
	}
	req.Header.Set("User-Agent", p.Agent)
	req.Header.Set("Accept", "application/rss+xml, application/xml, text/xml;q=0.9, */*;q=0.5")
	resp, err := p.HTTP.Do(req)
	if err != nil {
		return Record{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == 404 || resp.StatusCode == 410:
		return Record{}, ErrNotFound
	case resp.StatusCode >= 300:
		return Record{}, ErrUnavailable
	}
	var feed rssFeed
	dec := xml.NewDecoder(io.LimitReader(resp.Body, maxFeed))
	dec.Strict = false
	dec.CharsetReader = func(charset string, input io.Reader) (io.Reader, error) { return input, nil }
	if err := dec.Decode(&feed); err != nil || strings.TrimSpace(feed.Channel.Title) == "" {
		return Record{}, ErrNotFound
	}
	ch := feed.Channel
	show := Record{Provider: "rss", Kind: "podcast", ProviderID: id, Title: clean(ch.Title, 500), Overview: clean(stripTags(ch.Description), 20000),
		ImageURL: firstNonEmptyString(ch.Image.Href, ch.PlainImage.URL), ExternalIDs: map[string]string{"feed": id}, Details: map[string]any{}, ChildrenLoaded: true}
	if a := clean(ch.Author, 200); a != "" {
		show.Credits = []string{a}
	}
	for _, c := range ch.Categories {
		if t := clean(c.Text, 100); t != "" && len(show.Genres) < 5 {
			show.Genres = append(show.Genres, t)
		}
	}
	seen := map[string]bool{}
	var newest *time.Time
	for _, it := range ch.Items {
		if len(show.Children) >= 3000 {
			break
		}
		eid := RSSEpisodeID(id, firstNonEmptyString(strings.TrimSpace(it.GUID), it.Enclosure.URL, it.Title+"|"+it.PubDate))
		if seen[eid] || strings.TrimSpace(it.Title) == "" {
			continue
		}
		seen[eid] = true
		e := Record{Provider: "rss", Kind: "podcast_episode", ProviderID: eid, Title: clean(it.Title, 500), Overview: clean(stripTags(it.Description), 5000),
			ImageURL: it.Image.Href, ReleaseDate: rssDate(it.PubDate), RuntimeMinutes: rssDuration(it.Duration), ExternalIDs: map[string]string{}}
		if g := clean(it.GUID, 500); g != "" {
			e.ExternalIDs["guid"] = g
		}
		if n, err := strconv.Atoi(strings.TrimSpace(it.Episode)); err == nil && n > 0 && n < 1000000 {
			e.EpisodeNumber = &n
		}
		if n, err := strconv.Atoi(strings.TrimSpace(it.Season)); err == nil && n >= 0 && n < 100000 {
			e.SeasonNumber = &n
		}
		if e.ReleaseDate != nil && (newest == nil || e.ReleaseDate.After(*newest)) {
			newest = e.ReleaseDate
		}
		show.Children = append(show.Children, e)
	}
	show.Total = intPtr(max(len(show.Children), 1))
	show.ReleaseDate = newest
	return show, nil
}

func firstNonEmptyString(values ...string) string {
	for _, v := range values {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return ""
}

func rssDate(s string) *time.Time {
	s = strings.TrimSpace(s)
	for _, layout := range []string{time.RFC1123Z, time.RFC1123, "Mon, 2 Jan 2006 15:04:05 -0700", "Mon, 2 Jan 2006 15:04:05 MST", "2 Jan 2006 15:04:05 -0700", time.RFC3339} {
		if t, err := time.Parse(layout, s); err == nil {
			t = t.UTC()
			return &t
		}
	}
	return nil
}

// rssDuration reads HH:MM:SS, MM:SS or seconds, rounded up to whole minutes.
func rssDuration(s string) *int {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	seconds := 0
	for _, part := range strings.Split(s, ":") {
		n, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil || n < 0 {
			return nil
		}
		seconds = seconds*60 + n
	}
	if seconds <= 0 || seconds > 100*3600 {
		return nil
	}
	return intPtr((seconds + 59) / 60)
}

func stripTags(s string) string {
	var b strings.Builder
	depth := 0
	for _, r := range s {
		switch {
		case r == '<':
			depth++
		case r == '>' && depth > 0:
			depth--
		case depth == 0:
			b.WriteRune(r)
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

// RSSEpisodeID is the provider id of a feed episode, from its GUID or, without one, its
// enclosure address. Sync services that report either can name the same episode.
func RSSEpisodeID(feed, key string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(feed) + "\x00" + strings.TrimSpace(key)))
	return "g" + hex.EncodeToString(sum[:12])
}
