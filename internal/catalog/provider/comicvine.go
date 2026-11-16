package provider

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
)

// ComicVine covers comic volumes and their issues.
type ComicVine struct {
	Client *Client
	Key    string
}

func (ComicVine) Name() string    { return "comicvine" }
func (ComicVine) Kinds() []string { return []string{"comic"} }

type cvVolume struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Deck      string `json:"deck"`
	StartYear string `json:"start_year"`
	Count     int    `json:"count_of_issues"`
	Image     struct {
		Original string `json:"original_url"`
		Medium   string `json:"medium_url"`
	} `json:"image"`
	Publisher *struct{ Name string } `json:"publisher"`
	Issues    []struct {
		ID     int64  `json:"id"`
		Name   string `json:"name"`
		Number string `json:"issue_number"`
	} `json:"issues"`
}

func (c ComicVine) get(ctx context.Context, path string, q url.Values, out any) error {
	if c.Key == "" {
		return ErrNotConfigured
	}
	q.Set("api_key", c.Key)
	q.Set("format", "json")
	// Results are decoded only after the status code: failures carry an empty array.
	var envelope struct {
		Status  int             `json:"status_code"`
		Results json.RawMessage `json:"results"`
	}
	if err := c.Client.JSON(ctx, Request{URL: "https://comicvine.gamespot.com/api/" + path + "?" + q.Encode(), Retries: 1}, &envelope); err != nil {
		return err
	}
	switch envelope.Status {
	case 1:
		if json.Unmarshal(envelope.Results, out) != nil {
			return ErrInvalidPayload
		}
		return nil
	case 101:
		return ErrNotFound
	case 100:
		return ErrNotConfigured
	}
	return ErrUnavailable
}

func (c ComicVine) record(v cvVolume) Record {
	r := Record{Provider: "comicvine", Kind: "comic", ProviderID: strconv.FormatInt(v.ID, 10), Title: v.Name, Overview: v.Deck,
		ImageURL: v.Image.Medium, ReleaseDate: date(v.StartYear), Total: intPtr(v.Count), Unit: "items", Details: map[string]any{}, ExternalIDs: map[string]string{}}
	if v.Publisher != nil && v.Publisher.Name != "" {
		r.Credits = []string{v.Publisher.Name}
	}
	for _, issue := range v.Issues {
		title := issue.Name
		if title == "" {
			title = "#" + issue.Number
		}
		r.Children = append(r.Children, Record{Provider: "comicvine", Kind: "comic_issue", ProviderID: strconv.FormatInt(issue.ID, 10), Title: title,
			Details: map[string]any{"issue_number": clean(issue.Number, 20)}})
	}
	r.ChildrenLoaded = v.Issues != nil
	return r
}

func (c ComicVine) Search(ctx context.Context, kind, query string) ([]Record, error) {
	if kind != "comic" {
		return nil, ErrUnsupported
	}
	var volumes []cvVolume
	if err := c.get(ctx, "search/", url.Values{"resources": {"volume"}, "query": {query}, "limit": {"20"}, "field_list": {"id,name,deck,start_year,count_of_issues,image,publisher"}}, &volumes); err != nil {
		return nil, err
	}
	out := make([]Record, 0, len(volumes))
	for _, v := range volumes {
		out = append(out, c.record(v))
	}
	return out, nil
}

func (c ComicVine) Fetch(ctx context.Context, kind, id string) (Record, error) {
	if kind != "comic" {
		return Record{}, ErrUnsupported
	}
	if n, err := strconv.ParseInt(id, 10, 64); err != nil || n < 1 {
		return Record{}, ErrNotFound
	}
	var v cvVolume
	if err := c.get(ctx, "volume/4050-"+id+"/", url.Values{"field_list": {"id,name,deck,start_year,count_of_issues,image,publisher,issues"}}, &v); err != nil {
		return Record{}, err
	}
	if v.Issues == nil {
		v.Issues = v.Issues[:0:0]
	}
	return c.record(v), nil
}
