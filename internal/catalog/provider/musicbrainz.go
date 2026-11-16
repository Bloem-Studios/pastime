package provider

import (
	"context"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// MusicBrainz covers albums (release groups). It asks clients for about one request per
// second, so adapters keep calls minimal and the catalog caches the result.
type MusicBrainz struct{ Client *Client }

func (MusicBrainz) Name() string    { return "musicbrainz" }
func (MusicBrainz) Kinds() []string { return []string{"music"} }

type mbCredit struct {
	Name   string `json:"name"`
	Artist struct {
		ID string `json:"id"`
	} `json:"artist"`
}
type mbGroup struct {
	ID           string     `json:"id"`
	Title        string     `json:"title"`
	FirstRelease string     `json:"first-release-date"`
	PrimaryType  string     `json:"primary-type"`
	Credits      []mbCredit `json:"artist-credit"`
	Tags         []struct {
		Name  string `json:"name"`
		Count int    `json:"count"`
	} `json:"tags"`
	Releases []struct {
		ID string `json:"id"`
	} `json:"releases"`
}

func validMBID(id string) bool {
	return len(id) == 36 && strings.Count(id, "-") == 4 && strings.Trim(id, "0123456789abcdef-") == ""
}

func (m MusicBrainz) record(g mbGroup) Record {
	r := Record{
		Provider: "musicbrainz", Kind: "music", ProviderID: g.ID, Title: g.Title, ReleaseDate: date(g.FirstRelease),
		ImageURL: "https://coverartarchive.org/release-group/" + g.ID + "/front-500", ExternalIDs: map[string]string{}, Details: map[string]any{},
	}
	people := []map[string]any{}
	for _, c := range g.Credits {
		r.Credits = append(r.Credits, c.Name)
		if validMBID(c.Artist.ID) && len(people) < 10 {
			people = append(people, map[string]any{"id": c.Artist.ID, "name": clean(c.Name, 200), "role": "Artist"})
		}
	}
	if len(people) > 0 {
		r.Details["people"] = people
	}
	// Tags arrive alphabetically; the most agreed-on ones describe the album best.
	tags := append(g.Tags[:0:0], g.Tags...)
	sort.SliceStable(tags, func(i, j int) bool { return tags[i].Count > tags[j].Count })
	for _, t := range tags {
		if len(r.Genres) >= 6 {
			break
		}
		if name := clean(t.Name, 60); name != "" && strings.Trim(name, "0123456789+ ") != "" && strings.IndexFunc(name, unicode.IsDigit) < 0 {
			r.Genres = append(r.Genres, name)
		}
	}
	if g.PrimaryType != "" {
		r.Details["release_type"] = strings.ToLower(g.PrimaryType)
	}
	return r
}

func (m MusicBrainz) Search(ctx context.Context, kind, query string) ([]Record, error) {
	if kind != "music" {
		return nil, ErrUnsupported
	}
	var out struct {
		Groups []mbGroup `json:"release-groups"`
	}
	q := url.Values{"query": {query}, "fmt": {"json"}, "limit": {"20"}}
	if err := m.Client.JSON(ctx, Request{URL: "https://musicbrainz.org/ws/2/release-group?" + q.Encode(), Retries: 1}, &out); err != nil {
		return nil, err
	}
	records := make([]Record, 0, len(out.Groups))
	for _, g := range out.Groups {
		if validMBID(g.ID) {
			records = append(records, m.record(g))
		}
	}
	return records, nil
}

func (m MusicBrainz) Fetch(ctx context.Context, kind, id string) (Record, error) {
	if kind != "music" {
		return Record{}, ErrUnsupported
	}
	if !validMBID(id) {
		return Record{}, ErrNotFound
	}
	var g mbGroup
	if err := m.Client.JSON(ctx, Request{URL: "https://musicbrainz.org/ws/2/release-group/" + id + "?inc=artist-credits+releases+tags&fmt=json", Retries: 1}, &g); err != nil {
		return Record{}, err
	}
	r := m.record(g)
	// Tracks come from the first listed release; editions can differ. Each track is
	// keyed by its recording, which is how scrobblers name what was played.
	if len(g.Releases) > 0 && validMBID(g.Releases[0].ID) {
		var rel struct {
			Media []struct {
				Position   int `json:"position"`
				TrackCount int `json:"track-count"`
				Tracks     []struct {
					Position  int    `json:"position"`
					Title     string `json:"title"`
					Length    int64  `json:"length"`
					Recording struct {
						ID string `json:"id"`
					} `json:"recording"`
				} `json:"tracks"`
			} `json:"media"`
		}
		if err := m.Client.JSON(ctx, Request{URL: "https://musicbrainz.org/ws/2/release/" + g.Releases[0].ID + "?inc=recordings&fmt=json"}, &rel); err == nil {
			seen, counted := map[string]bool{}, 0
			for _, medium := range rel.Media {
				counted += medium.TrackCount
				for _, t := range medium.Tracks {
					if !validMBID(t.Recording.ID) || seen[t.Recording.ID] || len(r.Children) >= 500 {
						continue
					}
					seen[t.Recording.ID] = true
					disc, number := medium.Position, t.Position
					child := Record{Provider: "musicbrainz", Kind: "track", ProviderID: t.Recording.ID, Title: clean(t.Title, 500), SeasonNumber: &disc, EpisodeNumber: &number, ExternalIDs: map[string]string{}}
					if t.Length > 0 {
						child.RuntimeMinutes = intPtr(int((t.Length + 59999) / 60000))
					}
					if child.Title == "" {
						child.Title = "Track " + strconv.Itoa(number)
					}
					r.Children = append(r.Children, child)
				}
			}
			if len(r.Children) > 0 {
				r.Total, r.ChildrenLoaded = intPtr(len(r.Children)), true
			} else if counted > 0 {
				r.Total = intPtr(counted)
			}
		}
	}
	return r, nil
}

// FindExternal maps a release (one edition) or a recording to its release group.
func (m MusicBrainz) FindExternal(ctx context.Context, kind, scheme, value string) (string, error) {
	if kind != "music" || !validMBID(value) {
		return "", ErrNotFound
	}
	switch scheme {
	case "release":
		var rel struct {
			ReleaseGroup struct {
				ID string `json:"id"`
			} `json:"release-group"`
		}
		if err := m.Client.JSON(ctx, Request{URL: "https://musicbrainz.org/ws/2/release/" + value + "?inc=release-groups&fmt=json", Retries: 1}, &rel); err != nil {
			return "", err
		}
		if !validMBID(rel.ReleaseGroup.ID) {
			return "", ErrNotFound
		}
		return rel.ReleaseGroup.ID, nil
	case "recording":
		var rec struct {
			Releases []struct {
				ReleaseGroup struct {
					ID          string `json:"id"`
					PrimaryType string `json:"primary-type"`
				} `json:"release-group"`
			} `json:"releases"`
		}
		if err := m.Client.JSON(ctx, Request{URL: "https://musicbrainz.org/ws/2/recording/" + value + "?inc=releases+release-groups&fmt=json", Retries: 1}, &rec); err != nil {
			return "", err
		}
		// Prefer the album a track appeared on over singles and compilations.
		for _, want := range []string{"Album", "EP", ""} {
			for _, r := range rec.Releases {
				if validMBID(r.ReleaseGroup.ID) && (want == "" || r.ReleaseGroup.PrimaryType == want) {
					return r.ReleaseGroup.ID, nil
				}
			}
		}
		return "", ErrNotFound
	}
	return "", ErrNotFound
}

// Person is an artist page: who they are and their albums, newest first.
func (m MusicBrainz) Person(ctx context.Context, id string) (Person, error) {
	if !validMBID(id) {
		return Person{}, ErrNotFound
	}
	var a struct {
		Name           string `json:"name"`
		Type           string `json:"type"`
		Country        string `json:"country"`
		Disambiguation string `json:"disambiguation"`
		LifeSpan       struct {
			Begin string `json:"begin"`
			End   string `json:"end"`
		} `json:"life-span"`
	}
	if err := m.Client.JSON(ctx, Request{URL: "https://musicbrainz.org/ws/2/artist/" + id + "?fmt=json", Retries: 1}, &a); err != nil {
		return Person{}, err
	}
	var groups struct {
		Groups []mbGroup `json:"release-groups"`
	}
	q := url.Values{"artist": {id}, "type": {"album|ep"}, "limit": {"100"}, "fmt": {"json"}}
	if err := m.Client.JSON(ctx, Request{URL: "https://musicbrainz.org/ws/2/release-group?" + q.Encode(), Retries: 1}, &groups); err != nil {
		return Person{}, err
	}
	sort.SliceStable(groups.Groups, func(i, j int) bool { return groups.Groups[i].FirstRelease > groups.Groups[j].FirstRelease })
	kind := strings.TrimSpace(a.Type)
	if kind == "Group" {
		kind = "Band"
	}
	out := Person{Provider: "musicbrainz", ID: id, Name: clean(a.Name, 200), Biography: clean(strings.TrimSpace(strings.Join([]string{a.Disambiguation, a.Country}, " · ")), 500),
		KnownFor: clean(kind, 60), Birthday: date(a.LifeSpan.Begin), Deathday: date(a.LifeSpan.End)}
	out.Biography = strings.Trim(out.Biography, " ·")
	for _, g := range groups.Groups {
		if validMBID(g.ID) && len(out.Credits) < 100 {
			out.Credits = append(out.Credits, Credit{Record: m.record(g), Role: strings.ToLower(g.PrimaryType)})
		}
	}
	return out, nil
}

// Studio is not offered for music; labels are not browsed.
func (m MusicBrainz) Studio(ctx context.Context, kind, id string) (Studio, error) {
	return Studio{}, ErrNotFound
}
