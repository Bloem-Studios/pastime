package provider

import (
	"context"
	"errors"
	"net/url"
	"regexp"
	"strings"
)

// YouTube resolves single videos through YouTube's keyless oEmbed endpoint. Without an
// API key there is no keyword search: a search names a video by its address or id, and
// the record carries the title, channel and thumbnail.
type YouTube struct{ Client *Client }

func (YouTube) Name() string    { return "youtube" }
func (YouTube) Kinds() []string { return []string{"video"} }

var youTubeID = regexp.MustCompile(`^[A-Za-z0-9_-]{11}$`)

// YouTubeVideoID extracts a video id from an id, a watch, short, embed or live address.
func YouTubeVideoID(raw string) string {
	raw = strings.TrimSpace(raw)
	if youTubeID.MatchString(raw) {
		return raw
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return ""
	}
	host := strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
	host = strings.TrimPrefix(host, "m.")
	var id string
	switch host {
	case "youtu.be":
		id = strings.Trim(u.Path, "/")
	case "youtube.com", "music.youtube.com", "youtube-nocookie.com":
		if u.Path == "/watch" {
			id = u.Query().Get("v")
		} else {
			for _, prefix := range []string{"/shorts/", "/embed/", "/live/", "/v/"} {
				if strings.HasPrefix(u.Path, prefix) {
					id = strings.SplitN(strings.TrimPrefix(u.Path, prefix), "/", 2)[0]
				}
			}
		}
	}
	if youTubeID.MatchString(id) {
		return id
	}
	return ""
}

func (y YouTube) oembed(ctx context.Context, id string) (Record, error) {
	var v struct {
		Title        string `json:"title"`
		AuthorName   string `json:"author_name"`
		AuthorURL    string `json:"author_url"`
		ThumbnailURL string `json:"thumbnail_url"`
	}
	watch := "https://www.youtube.com/watch?v=" + id
	if err := y.Client.JSON(ctx, Request{URL: "https://www.youtube.com/oembed?" + url.Values{"url": {watch}, "format": {"json"}}.Encode(), Retries: 1}, &v); err != nil {
		// oEmbed answers 401 for private videos and those that cannot be embedded.
		if errors.Is(err, ErrNotConfigured) {
			return Record{}, ErrNotFound
		}
		return Record{}, err
	}
	if v.Title == "" {
		return Record{}, ErrInvalidPayload
	}
	r := Record{Provider: "youtube", Kind: "video", ProviderID: id, Title: v.Title, Unit: "minutes", ImageURL: safeURL(v.ThumbnailURL),
		ExternalIDs: map[string]string{"youtube": id}, Details: map[string]any{"url": watch}}
	if v.AuthorName != "" {
		r.Credits = []string{clean(v.AuthorName, 200)}
		r.Details["channel"] = clean(v.AuthorName, 200)
	}
	if channel := safeURL(v.AuthorURL); channel != "" {
		r.Details["channel_url"] = channel
	}
	return r, nil
}

// Search finds the one video named by an address or id.
func (y YouTube) Search(ctx context.Context, kind, query string) ([]Record, error) {
	if kind != "video" {
		return nil, ErrUnsupported
	}
	id := YouTubeVideoID(query)
	if id == "" {
		return []Record{}, nil
	}
	r, err := y.oembed(ctx, id)
	if err != nil {
		return nil, err
	}
	return []Record{r}, nil
}

func (y YouTube) Fetch(ctx context.Context, kind, id string) (Record, error) {
	if kind != "video" {
		return Record{}, ErrUnsupported
	}
	if !youTubeID.MatchString(id) {
		return Record{}, ErrNotFound
	}
	return y.oembed(ctx, id)
}
