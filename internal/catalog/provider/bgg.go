package provider

import (
	"context"
	"encoding/xml"
	"html"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// BGG covers board games through the BoardGameGeek XML API, which requires an
// application token.
type BGG struct {
	Client *Client
	Token  string
}

func (BGG) Name() string    { return "bgg" }
func (BGG) Kinds() []string { return []string{"boardgame"} }

type bggValue struct {
	Value string `xml:"value,attr"`
	Type  string `xml:"type,attr"`
}
type bggItem struct {
	ID        string     `xml:"id,attr"`
	Names     []bggValue `xml:"name"`
	Year      bggValue   `xml:"yearpublished"`
	Image     string     `xml:"image"`
	Thumbnail string     `xml:"thumbnail"`
	About     string     `xml:"description"`
	PlayTime  bggValue   `xml:"playingtime"`
	MinPlayer bggValue   `xml:"minplayers"`
	MaxPlayer bggValue   `xml:"maxplayers"`
	Links     []struct {
		Type  string `xml:"type,attr"`
		Value string `xml:"value,attr"`
	} `xml:"link"`
	Rating bggValue `xml:"statistics>ratings>average"`
}

func (b BGG) items(ctx context.Context, path string, q url.Values) ([]bggItem, error) {
	if b.Token == "" {
		return nil, ErrNotConfigured
	}
	body, err := b.Client.Do(ctx, Request{URL: "https://boardgamegeek.com/xmlapi2/" + path + "?" + q.Encode(), Accept: "application/xml", Header: http.Header{"Authorization": {"Bearer " + b.Token}}, Retries: 1})
	if err != nil {
		return nil, err
	}
	var out struct {
		Items []bggItem `xml:"item"`
	}
	if err = xml.Unmarshal(body, &out); err != nil {
		return nil, ErrInvalidPayload
	}
	return out.Items, nil
}

func (b BGG) record(v bggItem) Record {
	r := Record{Provider: "bgg", Kind: "boardgame", ProviderID: v.ID, Overview: html.UnescapeString(v.About), ImageURL: v.Image,
		ReleaseDate: date(v.Year.Value), Details: map[string]any{}, ExternalIDs: map[string]string{}}
	for _, n := range v.Names {
		if n.Type == "primary" || r.Title == "" {
			r.Title = n.Value
		}
	}
	if n, err := strconv.Atoi(v.PlayTime.Value); err == nil {
		r.RuntimeMinutes = intPtr(n)
	}
	if f, err := strconv.ParseFloat(v.Rating.Value, 64); err == nil {
		r.Rating = floatPtr(f)
	}
	if v.MinPlayer.Value != "" {
		r.Details["players"] = clean(v.MinPlayer.Value+"–"+v.MaxPlayer.Value, 20)
	}
	for _, l := range v.Links {
		switch l.Type {
		case "boardgamecategory":
			r.Genres = append(r.Genres, l.Value)
		case "boardgamedesigner":
			r.Credits = append(r.Credits, l.Value)
		}
	}
	return r
}

func (b BGG) Search(ctx context.Context, kind, query string) ([]Record, error) {
	if kind != "boardgame" {
		return nil, ErrUnsupported
	}
	found, err := b.items(ctx, "search", url.Values{"query": {query}, "type": {"boardgame"}})
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for i, v := range found {
		if i >= 20 {
			break
		}
		ids = append(ids, v.ID)
	}
	if len(ids) == 0 {
		return []Record{}, nil
	}
	// Search returns names only; one batched thing call supplies artwork and years.
	detailed, err := b.items(ctx, "thing", url.Values{"id": {strings.Join(ids, ",")}})
	if err != nil {
		return nil, err
	}
	out := make([]Record, 0, len(detailed))
	for _, v := range detailed {
		out = append(out, b.record(v))
	}
	return out, nil
}

func (b BGG) Fetch(ctx context.Context, kind, id string) (Record, error) {
	if kind != "boardgame" {
		return Record{}, ErrUnsupported
	}
	if n, err := strconv.ParseInt(id, 10, 64); err != nil || n < 1 {
		return Record{}, ErrNotFound
	}
	items, err := b.items(ctx, "thing", url.Values{"id": {id}, "stats": {"1"}})
	if err != nil {
		return Record{}, err
	}
	if len(items) == 0 {
		return Record{}, ErrNotFound
	}
	return b.record(items[0]), nil
}
