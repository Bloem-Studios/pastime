package provider

import (
	"context"
	"net/url"
	"strconv"
	"strings"
)

// MangaBaka is a keyless manga database that links each series to AniList, MyAnimeList
// and other sources. It is offered alongside AniList, which stays the manga default.
type MangaBaka struct{ Client *Client }

func (MangaBaka) Name() string    { return "mangabaka" }
func (MangaBaka) Kinds() []string { return []string{"manga"} }

const mangaBakaAPI = "https://api.mangabaka.org/v1"

type mbSeries struct {
	ID            int64    `json:"id"`
	State         string   `json:"state"`
	MergedWith    *int64   `json:"merged_with"`
	Title         string   `json:"title"`
	NativeTitle   *string  `json:"native_title"`
	Description   *string  `json:"description"`
	Year          *int     `json:"year"`
	TotalChapters *string  `json:"total_chapters"`
	Status        string   `json:"status"`
	Genres        []string `json:"genres"`
	Authors       []string `json:"authors"`
	Artists       []string `json:"artists"`
	Type          string   `json:"type"`
	Cover         struct {
		Raw struct {
			URL string `json:"url"`
		} `json:"raw"`
		X350 struct {
			X2 string `json:"x2"`
		} `json:"x350"`
	} `json:"cover"`
	Published struct {
		StartDate *string `json:"start_date"`
	} `json:"published"`
	Source map[string]struct {
		ID any `json:"id"`
	} `json:"source"`
}

func (m MangaBaka) record(v mbSeries) Record {
	r := Record{Provider: "mangabaka", Kind: "manga", ProviderID: strconv.FormatInt(v.ID, 10), Title: v.Title, Unit: "chapters",
		ImageURL: safeURL(v.Cover.X350.X2), Genres: v.Genres, ExternalIDs: map[string]string{}, Details: map[string]any{}}
	if r.ImageURL == "" {
		r.ImageURL = safeURL(v.Cover.Raw.URL)
	}
	if v.NativeTitle != nil {
		r.OriginalTitle = *v.NativeTitle
	}
	if v.Description != nil {
		r.Overview = strings.TrimSpace(*v.Description)
	}
	if v.Published.StartDate != nil {
		r.ReleaseDate = date(*v.Published.StartDate)
	}
	if r.ReleaseDate == nil && v.Year != nil && *v.Year > 0 {
		r.ReleaseDate = date(strconv.Itoa(*v.Year))
	}
	if v.TotalChapters != nil {
		if n, err := strconv.Atoi(strings.TrimSpace(*v.TotalChapters)); err == nil && n > 0 {
			r.Total = &n
		}
	}
	if v.Status == "completed" {
		r.ReleaseStatus = "finished"
	} else {
		r.ReleaseStatus = clean(v.Status, 60)
	}
	for _, name := range append(append([]string{}, v.Authors...), v.Artists...) {
		if name != "" && len(r.Credits) < 10 {
			r.Credits = append(r.Credits, name)
		}
	}
	// Other sources' ids let imports and links find the same series.
	for source, scheme := range map[string]string{"anilist": "anilist", "my_anime_list": "mal", "kitsu": "kitsu", "manga_updates": "mangaupdates"} {
		s, ok := v.Source[source]
		if !ok {
			continue
		}
		switch id := s.ID.(type) {
		case float64:
			if id > 0 {
				r.ExternalIDs[scheme] = strconv.FormatInt(int64(id), 10)
			}
		case string:
			if id != "" {
				r.ExternalIDs[scheme] = clean(id, 100)
			}
		}
	}
	if v.Type != "" {
		r.Details["format"] = clean(v.Type, 40)
	}
	return r
}

func (m MangaBaka) Search(ctx context.Context, kind, query string) ([]Record, error) {
	if kind != "manga" {
		return nil, ErrUnsupported
	}
	var resp struct {
		Data []mbSeries `json:"data"`
	}
	if err := m.Client.JSON(ctx, Request{URL: mangaBakaAPI + "/series/search?" + url.Values{"q": {query}, "limit": {"20"}}.Encode(), Retries: 1}, &resp); err != nil {
		return nil, err
	}
	out := make([]Record, 0, len(resp.Data))
	for _, v := range resp.Data {
		if v.State == "merged" || v.State == "deleted" {
			continue
		}
		out = append(out, m.record(v))
	}
	return out, nil
}

func (m MangaBaka) Fetch(ctx context.Context, kind, id string) (Record, error) {
	if kind != "manga" {
		return Record{}, ErrUnsupported
	}
	if n, err := strconv.ParseInt(id, 10, 64); err != nil || n < 1 {
		return Record{}, ErrNotFound
	}
	// A merged series points at the series that replaced it; follow it once.
	for range 2 {
		var resp struct {
			Data *mbSeries `json:"data"`
		}
		if err := m.Client.JSON(ctx, Request{URL: mangaBakaAPI + "/series/" + id, Retries: 1}, &resp); err != nil {
			return Record{}, err
		}
		if resp.Data == nil {
			return Record{}, ErrNotFound
		}
		if resp.Data.State == "merged" && resp.Data.MergedWith != nil {
			id = strconv.FormatInt(*resp.Data.MergedWith, 10)
			continue
		}
		return m.record(*resp.Data), nil
	}
	return Record{}, ErrNotFound
}
