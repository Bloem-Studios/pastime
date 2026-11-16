package provider

import (
	"context"
	"strings"
)

// Audnexus reads public audiobook metadata by an exact Audible ASIN. It does not
// perform title searches or construct Amazon artwork URLs.
type Audnexus struct{ Client *Client }

func (Audnexus) Name() string    { return "audnexus" }
func (Audnexus) Kinds() []string { return []string{"audiobook"} }

func validASIN(id string) bool {
	return len(id) == 10 && strings.Trim(id, "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789") == ""
}

func (a Audnexus) Search(ctx context.Context, kind, query string) ([]Record, error) {
	rec, err := a.Fetch(ctx, kind, query)
	if err != nil {
		return nil, err
	}
	return []Record{rec}, nil
}

func (a Audnexus) Fetch(ctx context.Context, kind, id string) (Record, error) {
	if kind != "audiobook" {
		return Record{}, ErrUnsupported
	}
	if !validASIN(id) {
		return Record{}, ErrNotFound
	}
	var out struct {
		ASIN    string `json:"asin"`
		Title   string `json:"title"`
		Image   string `json:"image"`
		Authors []struct {
			Name string `json:"name"`
		} `json:"authors"`
	}
	if err := a.Client.JSON(ctx, Request{URL: "https://api.audnex.us/books/" + id}, &out); err != nil {
		return Record{}, err
	}
	if out.ASIN != id || strings.TrimSpace(out.Title) == "" {
		return Record{}, ErrNotFound
	}
	rec := Record{Provider: a.Name(), Kind: kind, ProviderID: id, Title: out.Title, ImageURL: safeURL(out.Image), ExternalIDs: map[string]string{"asin": id}}
	for _, author := range out.Authors {
		rec.Credits = append(rec.Credits, author.Name)
	}
	return rec, nil
}
