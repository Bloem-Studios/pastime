package provider

import (
	"context"
	"net/url"
	"strconv"
	"strings"
)

// ITunes covers podcast shows and their episode lists through Apple's public lookup API.
type ITunes struct{ Client *Client }

func (ITunes) Name() string    { return "itunes" }
func (ITunes) Kinds() []string { return []string{"podcast"} }

type itunesResult struct {
	Kind           string `json:"kind"`
	WrapperType    string `json:"wrapperType"`
	CollectionID   int64  `json:"collectionId"`
	TrackID        int64  `json:"trackId"`
	CollectionName string `json:"collectionName"`
	TrackName      string `json:"trackName"`
	ArtistName     string `json:"artistName"`
	Artwork600     string `json:"artworkUrl600"`
	Artwork160     string `json:"artworkUrl160"`
	FeedURL        string `json:"feedUrl"`
	ReleaseDate    string `json:"releaseDate"`
	TrackCount     int    `json:"trackCount"`
	Genre          string `json:"primaryGenreName"`
	Description    string `json:"description"`
	DurationMillis int64  `json:"trackTimeMillis"`
	EpisodeGUID    string `json:"episodeGuid"`
}

func (i ITunes) lookup(ctx context.Context, path string, q url.Values) ([]itunesResult, error) {
	var out struct {
		Results []itunesResult `json:"results"`
	}
	err := i.Client.JSON(ctx, Request{URL: "https://itunes.apple.com/" + path + "?" + q.Encode(), Accept: "application/json, text/javascript", Retries: 1}, &out)
	return out.Results, err
}

func (i ITunes) show(v itunesResult) Record {
	r := Record{
		Provider: "itunes", Kind: "podcast", ProviderID: strconv.FormatInt(v.CollectionID, 10), Title: v.CollectionName,
		ImageURL: v.Artwork600, ReleaseDate: date(v.ReleaseDate), Total: intPtr(v.TrackCount),
		Credits: []string{v.ArtistName}, ExternalIDs: map[string]string{}, Details: map[string]any{},
	}
	if v.Genre != "" {
		r.Genres = []string{v.Genre}
	}
	if strings.HasPrefix(v.FeedURL, "https://") || strings.HasPrefix(v.FeedURL, "http://") {
		r.ExternalIDs["feed"] = clean(v.FeedURL, 1000)
	}
	return r
}

func (i ITunes) Search(ctx context.Context, kind, query string) ([]Record, error) {
	if kind != "podcast" {
		return nil, ErrUnsupported
	}
	results, err := i.lookup(ctx, "search", url.Values{"media": {"podcast"}, "entity": {"podcast"}, "term": {query}, "limit": {"20"}})
	if err != nil {
		return nil, err
	}
	out := make([]Record, 0, len(results))
	for _, v := range results {
		if v.CollectionID > 0 && v.CollectionName != "" {
			out = append(out, i.show(v))
		}
	}
	return out, nil
}

// Fetch returns the show with its most recent 200 episodes; Apple does not page further.
func (i ITunes) Fetch(ctx context.Context, kind, id string) (Record, error) {
	if kind != "podcast" {
		return Record{}, ErrUnsupported
	}
	if n, err := strconv.ParseInt(id, 10, 64); err != nil || n < 1 {
		return Record{}, ErrNotFound
	}
	results, err := i.lookup(ctx, "lookup", url.Values{"id": {id}, "entity": {"podcastEpisode"}, "limit": {"200"}})
	if err != nil {
		return Record{}, err
	}
	var show *Record
	episodes := []Record{}
	for _, v := range results {
		switch {
		case v.WrapperType == "track" && v.Kind == "podcast" && show == nil:
			r := i.show(v)
			show = &r
		case v.WrapperType == "podcastEpisode" && v.TrackID > 0:
			e := Record{
				Provider: "itunes", Kind: "podcast_episode", ProviderID: strconv.FormatInt(v.TrackID, 10), Title: v.TrackName,
				Overview: v.Description, ImageURL: v.Artwork600, ReleaseDate: date(v.ReleaseDate), ExternalIDs: map[string]string{},
			}
			if v.DurationMillis > 0 {
				e.RuntimeMinutes = intPtr(int((v.DurationMillis + 59999) / 60000))
			}
			if v.EpisodeGUID != "" {
				e.ExternalIDs["guid"] = clean(v.EpisodeGUID, 500)
			}
			episodes = append(episodes, e)
		}
	}
	if show == nil {
		return Record{}, ErrNotFound
	}
	show.Children, show.ChildrenLoaded = episodes, true
	if show.Total == nil || *show.Total < len(episodes) {
		show.Total = intPtr(len(episodes))
	}
	return *show, nil
}
